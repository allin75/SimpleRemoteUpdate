package main

import (
	"io"
	"os"
	"sync"
)

type dynamicLogWriter struct {
	mu   sync.Mutex
	file *os.File
}

func newDynamicLogWriter(path string) (*dynamicLogWriter, error) {
	w := &dynamicLogWriter{}
	if err := w.SwitchFile(path); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dynamicLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, _ = os.Stdout.Write(p)
	if w.file == nil {
		return len(p), nil
	}
	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	return len(p), nil
}

func (w *dynamicLogWriter) SwitchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.mu.Lock()
	old := w.file
	w.file = f
	w.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (w *dynamicLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

var _ io.Writer = (*dynamicLogWriter)(nil)

