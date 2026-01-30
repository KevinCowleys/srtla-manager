//go:build windows

package process

// ReloadIPs updates the bind IPs file and restarts SRTLA on Windows.
// Since Windows doesn't support SIGHUP, we stop the process and let the
// health monitor (monitorPipelineHealth) restart it automatically.
func (h *SRTLAHandler) ReloadIPs(ips []string) error {
	if err := h.writeIPsFile(ips); err != nil {
		return err
	}

	h.mu.RLock()
	state := h.stats.State
	h.mu.RUnlock()

	if state == SRTLAStopped {
		return nil
	}

	// Stop the process - the health monitor will restart it with new IPs
	h.proc.Stop()

	h.mu.Lock()
	h.stats.State = SRTLAStarting
	h.mu.Unlock()

	return nil
}
