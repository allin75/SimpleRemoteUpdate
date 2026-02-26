//go:build !windows

package main

import (
	"context"
	"errors"
	"time"
)

func stopServiceImpl(_ context.Context, _ string, _ time.Duration) error {
	return errors.New("当前平台不支持 windows 服务控制")
}

func startServiceImpl(_ context.Context, _ string, _ time.Duration) error {
	return errors.New("当前平台不支持 windows 服务控制")
}

