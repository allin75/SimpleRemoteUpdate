//go:build !windows

package main

import (
	"context"
	"errors"
	"syscall"
	"time"
)

func stopServiceImpl(_ context.Context, _ string, _ time.Duration) error {
	return errors.New("当前平台不支持 windows 服务控制")
}

func startServiceImpl(_ context.Context, _ string, _ time.Duration) error {
	return errors.New("当前平台不支持 windows 服务控制")
}

func serviceExists(_ string) (bool, error) {
	return false, errors.New("当前平台不支持 windows 服务控制")
}

func createService(_ string, _ ServiceInstallConfig) error {
	return errors.New("当前平台不支持 windows 服务控制")
}

func selfUpdateWorkerSysProcAttr() *syscall.SysProcAttr {
	return nil
}
