package zip_streamer

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
)

const NUM_RETRIES = 3

type ZipStream struct {
	entries           chan *FileEntry
	destination       io.Writer
	CompressionMethod uint16
}

func NewZipStream(entries chan *FileEntry, w io.Writer) (*ZipStream, error) {
	z := ZipStream{
		entries:     entries,
		destination: w,
		// Default to no compression to save CPU. Also ideal for streaming.
		CompressionMethod: zip.Store,
	}

	return &z, nil
}

// TODO: consider using https://github.com/hashicorp/go-retryablehttp
func retryableGet(url string) (*http.Response, error) {
	var err error

	for i := 0; i < NUM_RETRIES; i++ {
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(1)
			continue
		} else {
			return resp, nil
		}
	}

	return nil, err
}

func (z *ZipStream) StreamAllFiles(context context.Context) error {
	hub := sentry.GetHubFromContext(context)

	zipWriter := zip.NewWriter(z.destination)
	success := 0

	for entry := range z.entries {
		resp, err := retryableGet(entry.Url().String())
		if err != nil {
			if hub != nil {
				hub.CaptureException(err)
			}
			// TODO continue?
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if hub != nil {
				hub.CaptureMessage(fmt.Sprintf("Received status %d for URL %s", resp.StatusCode, entry.Url().String()))
			}
			// TODO continue?
			continue
		}

		header := &zip.FileHeader{
			Name:     entry.ZipPath(),
			Method:   z.CompressionMethod,
			Modified: time.Now(),
		}
		entryWriter, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		_, err = io.Copy(entryWriter, resp.Body)
		if err != nil {
			return err
		}

		zipWriter.Flush()
		flushingWriter, ok := z.destination.(http.Flusher)
		if ok {
			flushingWriter.Flush()
		}

		success++
	}

	if success == 0 {
		return errors.New("empty file - all files failed")
	}

	return zipWriter.Close()
}
