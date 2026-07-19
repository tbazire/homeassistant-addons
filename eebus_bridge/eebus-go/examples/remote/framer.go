package main

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"golang.org/x/exp/jsonrpc2"
)

type NewlineFramer struct{}
type newlineReader struct{ in *bufio.Reader }
type newlineWriter struct{ out io.Writer }

func (NewlineFramer) Reader(rw io.Reader) jsonrpc2.Reader {
	return &newlineReader{in: bufio.NewReader(rw)}
}

func (f NewlineFramer) Writer(rw io.Writer) jsonrpc2.Writer {
	return &newlineWriter{out: rw}
}

func (r *newlineReader) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	var total int64

	// read a line
	line, err := r.in.ReadBytes('\n')
	total += int64(len(line))
	if err != nil {
		return nil, total, fmt.Errorf("failed reading line: %w", err)
	}

	msg, err := jsonrpc2.DecodeMessage(line[:total-1])
	return msg, total, err
}

func (w *newlineWriter) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %v", err)
	}
	n, err := w.out.Write(data)
	total := int64(n)
	if err == nil {
		n, err = w.out.Write([]byte("\n"))
		total += int64(n)
	}
	return total, err
}
