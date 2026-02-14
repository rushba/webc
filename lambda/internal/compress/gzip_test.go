package compress

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"testing"
)

func TestGzipCompress(t *testing.T) {
	data := []byte("hello world")
	compressed, err := gzipNaive(data)
	if err != nil {
		t.Fatalf("gzipCompress() error = %v", err)
	}

	if len(compressed) == 0 {
		t.Error("gzipCompress() returned empty result")
	}

	// Compressed data should have gzip magic number
	if compressed[0] != 0x1f || compressed[1] != 0x8b {
		t.Error("gzipCompress() output missing gzip magic number")
	}

	// Empty input should work
	compressed, err = gzipNaive([]byte{})
	if err != nil {
		t.Fatalf("gzipCompress(empty) error = %v", err)
	}
	if len(compressed) == 0 {
		t.Error("gzipCompress(empty) returned empty result")
	}
}

func TestGzipCompressPooled(t *testing.T) {
	data := []byte("hello world from pooled compressor")
	compressed, err := Gzip(data)
	if err != nil {
		t.Fatalf("gzipCompressPooled() error = %v", err)
	}

	if compressed[0] != 0x1f || compressed[1] != 0x8b {
		t.Error("gzipCompressPooled() output missing gzip magic number")
	}

	// Verify decompression matches original
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	decompressed, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	_ = r.Close()
	if !bytes.Equal(decompressed, data) {
		t.Error("decompressed data doesn't match original")
	}
}

func TestGzipCompressPooledConcurrent(t *testing.T) {
	// Verify pool safety under concurrent use
	data := []byte("concurrent test data")
	errs := make(chan error, 10)

	for range 10 {
		go func() {
			compressed, err := Gzip(data)
			if err != nil {
				errs <- err
				return
			}
			r, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				errs <- err
				return
			}
			got, err := io.ReadAll(r)
			_ = r.Close()
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, data) {
				errs <- fmt.Errorf("data mismatch")
				return
			}
			errs <- nil
		}()
	}

	for range 10 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent gzipCompressPooled failed: %v", err)
		}
	}
}
