//go:build !windows

package spool

import "errors"

func NewSystem() (Spooler, error) {
	return nil, errors.New("the system spooler is only supported on Windows; run with -file-spool <dir> instead")
}
