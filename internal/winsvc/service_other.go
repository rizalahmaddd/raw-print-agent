//go:build !windows

package winsvc

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("Windows service management is only available on Windows")

func IsService() bool                                  { return false }
func RunService(func(ctx context.Context) error) error { return errUnsupported }
func Install(string, ...string) error                  { return errUnsupported }
func Uninstall() error                                 { return errUnsupported }
func Start() error                                     { return errUnsupported }
func Stop() error                                      { return errUnsupported }
func AutoStartEnabled() (bool, error)                  { return false, errUnsupported }
func SetAutoStart(bool) error                          { return errUnsupported }
func AllowLocalNetworkAccess([]string) error           { return errUnsupported }
