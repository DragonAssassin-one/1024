package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStreamModelResponsePreservesChunks(t *testing.T) {
	var output bytes.Buffer
	err := streamModelResponse(&output, strings.NewReader("event: one\n\ndata: two\n\n"), []byte("secret"), 1024)
	if err != nil || output.String() != "event: one\n\ndata: two\n\n" {
		t.Fatalf("stream: %q, %v", output.String(), err)
	}
}

func TestStreamModelResponseDetectsCredentialAcrossChunks(t *testing.T) {
	var output bytes.Buffer
	reader := &chunkReader{chunks: [][]byte{
		[]byte("prefix secret-"),
		[]byte("token suffix"),
	}}
	err := streamModelResponse(&output, reader, []byte("secret-token"), 1024)
	if err == nil || output.String() != "prefix " {
		t.Fatalf("credential leak: %q, %v", output.String(), err)
	}
}

func TestStreamModelResponseRejectsLargeOrUnreadableResponse(t *testing.T) {
	var output bytes.Buffer
	if err := streamModelResponse(&output, strings.NewReader("12345"), []byte("key"), 4); err == nil {
		t.Fatal("large response accepted")
	}
	if err := streamModelResponse(&output, errorReader{}, []byte("key"), 1024); err == nil {
		t.Fatal("reader error swallowed")
	}
}

func TestGuardedResponseWriterFlushesSafeSuffix(t *testing.T) {
	var output bytes.Buffer
	writer := &guardedResponseWriter{writer: &output, token: []byte("secret"), limit: 100}
	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil || output.String() != "hello" {
		t.Fatalf("flush: %q, %v", output.String(), err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
}

type chunkReader struct {
	chunks [][]byte
}

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	copy(buffer, chunk)
	return len(chunk), nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("upstream read failed")
}
