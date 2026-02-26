//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func stopServiceImpl(ctx context.Context, name string, timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Stopped {
		return nil
	}

	st, err = s.Control(svc.Stop)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "service has not been started") {
			return nil
		}
		return err
	}

	deadline := time.Now().Add(timeout)
	for st.State != svc.Stopped {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("等待服务停止超时")
		}
		time.Sleep(500 * time.Millisecond)
		st, err = s.Query()
		if err != nil {
			return err
		}
	}
	return nil
}

func startServiceImpl(ctx context.Context, name string, timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Running {
		return nil
	}

	if err := s.Start(); err != nil {
		lower := strings.ToLower(err.Error())
		if !strings.Contains(lower, "already running") {
			return fmt.Errorf("启动服务失败: %w", err)
		}
	}

	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("等待服务启动超时")
		}
		st, err = s.Query()
		if err != nil {
			return err
		}
		if st.State == svc.Running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

