//go:build !windows

package process

import "syscall"

func (h *SRTLAHandler) ReloadIPs(ips []string) error {
	if err := h.writeIPsFile(ips); err != nil {
		return err
	}
	return h.proc.Signal(syscall.SIGHUP)
}
