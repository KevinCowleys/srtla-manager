// Main application entry point
import { CONFIG } from './config.js';
import { escapeHtml, formatBytes, getSignalBars, getWifiSignalBars, copyToClipboard, showNotification } from './utils.js';
import { API } from './api.js';
import { WebSocketManager } from './websocket.js';
import { ChartManager } from './chart.js';
import { ModemManager } from './modem.js';
import { USBNetManager } from './usbnet.js';
import { NetworkManager } from './network.js';
import { WiFiManager } from './wifi.js';
import { CameraManager } from './camera.js';
import { USBCamManager } from './usbcam.js';
import { UpdateManager } from './updates.js';

class SRTLAManager {
    constructor() {
        this.logs = [];
        this.maxLogs = CONFIG.maxLogs;

        // Initialize managers
        this.chart = new ChartManager();
        this.updates = new UpdateManager();
        this.ws = new WebSocketManager((msg) => this.handleMessage(msg));
        this.modem = new ModemManager();
        this.usbnet = new USBNetManager();
        this.network = new NetworkManager();
        this.wifi = new WiFiManager();
        this.camera = new CameraManager();
        this.usbcam = new USBCamManager();

        this.init();
    }

    init() {
        this.chart.init();
        this.loadConfig();
        this.network.loadDependencies();
        this.network.loadInterfaces();
        this.usbnet.load();
        this.wifi.load();
        this.modem.load();
        this.loadLogs();
        this.camera.load();
        this.usbcam.load();
        this.updates.load();
        this.ws.connect();
        this.setupEventListeners();
        this.navigateFromHash();
        this.updateRTMPUrl();
        setInterval(() => this.wifi.updateStatus(), CONFIG.wifiRefreshInterval);
    }

    async loadConfig() {
        try {
            const config = await API.get('/api/config');
            document.getElementById('rtmpPort').value = config.rtmp?.listen_port || 1935;
            document.getElementById('streamKey').value = config.rtmp?.stream_key || 'live';
            document.getElementById('srtlaEnabled').checked = config.srtla?.enabled !== false;
            document.getElementById('remoteHost').value = config.srtla?.remote_host || 'localhost';
            document.getElementById('remotePort').value = config.srtla?.remote_port || 5000;
            document.getElementById('bindIPs').value = (config.srtla?.bind_ips || []).join('\\n');
            document.getElementById('ipsFilePath').value = config.srtla?.bind_ips_file || '';
            this.updateRTMPUrl();
        } catch (e) {
            console.error('Failed to load config:', e);
        }
    }

    async saveConfig() {
        const bindIPsText = document.getElementById('bindIPs').value.trim();
        const bindIPs = bindIPsText.split('\\n').map(s => s.trim()).filter(s => s);

        const ipRegex = /^(\\d{1,3}\\.){3}\\d{1,3}$/;
        const invalidIPs = bindIPs.filter(ip => {
            if (!ipRegex.test(ip)) return true;
            return ip.split('.').some(o => parseInt(o) > 255);
        });

        if (invalidIPs.length > 0) {
            showNotification(`Invalid IP addresses:\\n${invalidIPs.join('\\n')}`, 'error');
            return;
        }

        if (bindIPs.length > 0) {
            try {
                const interfaces = await API.get('/api/system/interfaces');
                const systemIPs = interfaces.flatMap(iface => iface.ips);
                const missingIPs = bindIPs.filter(ip => !systemIPs.includes(ip));
                if (missingIPs.length > 0) {
                    showNotification(`IPs don't exist on system:\\n${missingIPs.join('\\n')}`, 'error');
                    return;
                }
            } catch (e) {
                console.error('Failed to validate IPs:', e);
                showNotification('Failed to validate IPs', 'error');
                return;
            }
        }

        try {
            // Get current config to preserve fields not in the UI
            const currentConfig = await API.get('/api/config');
            
            const config = {
                rtmp: {
                    listen_port: parseInt(document.getElementById('rtmpPort').value),
                    stream_key: document.getElementById('streamKey').value
                },
                srt: { local_port: 6000 },
                srtla: {
                    enabled: document.getElementById('srtlaEnabled').checked,
                    binary_path: 'srtla_send',
                    remote_host: document.getElementById('remoteHost').value,
                    remote_port: parseInt(document.getElementById('remotePort').value),
                    bind_ips: bindIPs,
                    bind_ips_file: document.getElementById('ipsFilePath').value.trim(),
                    classic: currentConfig.srtla?.classic || false,
                    no_quality: currentConfig.srtla?.no_quality || false,
                    exploration: currentConfig.srtla?.exploration || false
                },
                web: { port: 8080 },
                logging: currentConfig.logging || {
                    debug: false,
                    file_path: 'logs/srtla-manager.log',
                    max_size_mb: 10,
                    max_backups: 3
                }
            };

            await API.put('/api/config', config);
            showNotification('Configuration saved');
        } catch (e) {
            showNotification(e.message || 'Failed to save configuration', 'error');
        }
    }

    handleMessage(msg) {
        switch (msg.type) {
            case 'stats': this.updateStats(msg.data); break;
            case 'log': this.addLog(msg.data); break;
            case 'state': this.updateState(msg.data); break;
            case 'modems': this.modem.update(msg.data); break;
            case 'usbnet': this.usbnet.update(msg.data); break;
            case 'wifi': this.wifi.updateStatus(); break;
            case 'srtla_install': this.handleSRTLAInstallProgress(msg.data); break;
        }
    }

    handleSRTLAInstallProgress(data) {
        const { level, message } = data;
        console.log(`[SRTLA Install ${level}]: ${message}`);
        
        // Show notification for important updates
        if (level === 'error') {
            showNotification('Installation Error', message, 'error');
        } else if (level === 'success') {
            showNotification('Installation Success', message, 'success');
        } else {
            showNotification('Installation Progress', message, 'info');
        }
    }

    updateStats(data) {
        // Update pipeline mode indicator and button states
        if (data.pipeline_mode) {
            this.updatePipelineMode(data.pipeline_mode);
        }

        if (data.ffmpeg) {
            const state = data.ffmpeg.stale ? 'stalled' : (data.ffmpeg.state || 'Unknown');
            const el = document.getElementById('ffmpegStatus');
            if (el) {
                el.textContent = state;
                el.className = 'status-indicator ' + this.getStatusClass(state);
            }
            const br = document.getElementById('ffmpegBitrate');
            if (br) br.textContent = `${(data.ffmpeg.bitrate || 0).toFixed(0)} kbps`;
            const fps = document.getElementById('ffmpegFps');
            if (fps) fps.textContent = (data.ffmpeg.fps || 0).toFixed(1);
        }

        if (data.srtla) {
            const state = data.srtla.stale ? 'stalled' : (data.srtla.state || 'Unknown');
            const el = document.getElementById('srtlaStatus');
            if (el) {
                el.textContent = state;
                el.className = 'status-indicator ' + this.getStatusClass(state);
            }
            const br = document.getElementById('srtlaBitrate');
            if (br) br.textContent = `${(data.srtla.bitrate || 0).toFixed(2)} Mbps`;
            const conn = document.getElementById('srtlaConnections');
            if (conn) conn.textContent = data.srtla.connections?.length || 0;
        }

        this.chart.update(data.ffmpeg?.bitrate, data.srtla?.bitrate);
    }

    updatePipelineMode(mode) {
        const el = document.getElementById('pipelineMode');
        if (el) {
            const labels = { idle: 'Idle', receiving: 'Receiving', streaming: 'Streaming' };
            el.textContent = labels[mode] || mode;
            el.className = 'pipeline-mode pipeline-' + mode;
        }
        const startBtn = document.getElementById('startBtn');
        const stopBtn = document.getElementById('stopBtn');
        if (startBtn) startBtn.disabled = mode === 'streaming';
        if (stopBtn) stopBtn.disabled = mode !== 'streaming';
    }

    updateState(data) {
        const isRunning = data.ffmpeg === 'running' || data.srtla === 'running';
        const startBtn = document.getElementById('startBtn');
        const stopBtn = document.getElementById('stopBtn');
        if (startBtn) startBtn.disabled = isRunning;
        if (stopBtn) stopBtn.disabled = !isRunning;
    }

    getStatusClass(state) {
        const map = {
            stopped: 'stopped', waiting: 'waiting', starting: 'waiting',
            connected: 'connected', streaming: 'streaming', registering: 'waiting',
            running: 'streaming', error: 'error', stalled: 'error'
        };
        return map[state] || 'stopped';
    }

    addLog(data) {
        const showFfmpeg = document.getElementById('filterFfmpeg')?.checked ?? true;
        const showSrtla = document.getElementById('filterSrtla')?.checked ?? true;
        if (data.source === 'ffmpeg' && !showFfmpeg) return;
        if (data.source === 'srtla_send' && !showSrtla) return;
        
        this.logs.push(data);
        if (this.logs.length > this.maxLogs) this.logs.shift();
        this.renderLogs();
    }

    renderLogs() {
        const container = document.getElementById('logContainer');
        if (!container) return;

        const showFfmpeg = document.getElementById('filterFfmpeg')?.checked ?? true;
        const showSrtla = document.getElementById('filterSrtla')?.checked ?? true;
        const filtered = this.logs.filter(log => {
            if (log.source === 'ffmpeg' && !showFfmpeg) return false;
            if (log.source === 'srtla_send' && !showSrtla) return false;
            return true;
        });

        const wasAtBottom = container.scrollHeight - container.scrollTop <= container.clientHeight + 50;
        container.innerHTML = filtered.map(log =>
            `<div class="log-line log-${log.source}"><span class="log-source">[${log.source}]</span> ${escapeHtml(log.line)}</div>`
        ).join('');
        if (wasAtBottom) container.scrollTop = container.scrollHeight;
    }

    async loadLogs() {
        try {
            const logs = await API.get('/api/logs');
            if (logs && Array.isArray(logs)) {
                this.logs = logs;
                this.renderLogs();
            }
            
            // Load debug mode status
            const debugStatus = await API.get('/api/debug');
            const debugToggle = document.getElementById('debugModeToggle');
            if (debugToggle && debugStatus) {
                debugToggle.checked = debugStatus.debug || false;
            }
        } catch (e) {
            console.error('Failed to load logs:', e);
        }
    }

    async downloadLogs() {
        try {
            window.location.href = '/api/logs/download';
            showNotification('Downloading logs...', 'success');
        } catch (e) {
            showNotification(`Failed to download logs: ${e.message}`, 'error');
        }
    }

    async toggleDebugMode(enabled) {
        try {
            await API.post('/api/debug', { debug: enabled });
            showNotification(`Debug mode ${enabled ? 'enabled' : 'disabled'}`, 'success');
        } catch (e) {
            showNotification(`Failed to toggle debug mode: ${e.message}`, 'error');
            // Revert toggle on error
            const debugToggle = document.getElementById('debugModeToggle');
            if (debugToggle) {
                debugToggle.checked = !enabled;
            }
        }
    }

    async startStream() {
        try {
            document.getElementById('startBtn').disabled = true;
            await API.post('/api/stream/start');
            document.getElementById('stopBtn').disabled = false;
            showNotification('Stream started');
        } catch (e) {
            document.getElementById('startBtn').disabled = false;
            showNotification(`Failed to start: ${e.message}`, 'error');
        }
    }

    async stopStream() {
        try {
            document.getElementById('stopBtn').disabled = true;
            await API.post('/api/stream/stop');
            document.getElementById('startBtn').disabled = false;
            showNotification('Stream stopped');
        } catch (e) {
            document.getElementById('stopBtn').disabled = false;
            showNotification('Failed to stop stream', 'error');
        }
    }

    async updateRTMPUrl() {
        const port = document.getElementById('rtmpPort')?.value || 1935;
        const el = document.getElementById('rtmpUrl');
        
        if (!el) return;
        
        try {
            const interfaces = await API.get('/api/system/interfaces');
            // Find the first up interface with an IP (prefer non-loopback)
            const iface = interfaces.find(i => i.is_up && i.ips && i.ips.length > 0 && !i.is_loopback) ||
                         interfaces.find(i => i.is_up && i.ips && i.ips.length > 0);
            const host = iface && iface.ips[0] ? iface.ips[0] : 'localhost';
            el.textContent = `rtmp://${host}:${port}/<STREAM_KEY>`;
        } catch (e) {
            console.error('Failed to load interfaces:', e);
            el.textContent = `rtmp://localhost:${port}/<STREAM_KEY>`;
        }
        
        this.updateCameraConnectionDetails();
    }

    updateCameraConnectionDetails() {
        const port = document.getElementById('rtmpPort')?.value || 1935;
        const key = document.getElementById('streamKey')?.value || 'live';
        const portEl = document.getElementById('cameraPort');
        const keyEl = document.getElementById('cameraStreamKey');
        if (portEl) portEl.value = port;
        if (keyEl) keyEl.value = key;

        this.detectDeviceIP().then(ip => {
            if (ip) {
                const addrEl = document.getElementById('cameraServerAddr');
                const urlEl = document.getElementById('cameraFullURL');
                if (addrEl) addrEl.value = ip;
                if (urlEl) urlEl.value = `rtmp://${ip}:${port}/${key}`;
            }
        });
    }

    async detectDeviceIP() {
        try {
            const interfaces = await API.get('/api/system/interfaces');
            for (const iface of interfaces) {
                if (!iface.is_loopback && iface.ips?.length > 0) {
                    const isUSB = /usb|rndis|^u/i.test(iface.name);
                    if (isUSB && iface.is_up) return iface.ips[0];
                }
            }
            for (const iface of interfaces) {
                if (!iface.is_loopback && iface.ips?.length > 0 && iface.is_up) {
                    return iface.ips[0];
                }
            }
        } catch (e) {
            console.error('Failed to detect IP:', e);
        }
        return null;
    }

    copyField(fieldId) {
        const field = document.getElementById(fieldId);
        if (!field || !field.value) {
            showNotification('Field is empty', 'error');
            return;
        }
        copyToClipboard(field.value, (success) => {
            if (success) showNotification(`Copied: ${field.value}`);
            else showNotification('Failed to copy', 'error');
        });
    }

    switchTab(tabName) {
        document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
        document.querySelectorAll('.tab-button').forEach(b => b.classList.remove('active'));
        const tab = document.getElementById(tabName);
        if (tab) tab.classList.add('active');
        const btn = document.querySelector(`.tab-button[href="#${tabName}"]`);
        if (btn) btn.classList.add('active');
    }

    navigateFromHash() {
        const hash = window.location.hash.replace('#', '') || 'dashboard';
        this.switchTab(hash);
    }

    toggleSecretField(fieldId) {
        const field = document.getElementById(fieldId);
        if (!field) return;
        const isPassword = field.type === 'password';
        field.type = isPassword ? 'text' : 'password';
        const btn = document.querySelector(`[data-target="${fieldId}"]`);
        if (btn) {
            btn.classList.toggle('revealed', isPassword);
        }
    }

    setupEventListeners() {
        document.querySelectorAll('.tab-button').forEach(btn =>
            btn.addEventListener('click', (e) => {
                e.preventDefault();
                const tab = btn.getAttribute('href').replace('#', '');
                window.location.hash = tab;
            })
        );

        window.addEventListener('hashchange', () => this.navigateFromHash());

        document.querySelectorAll('.btn-toggle-secret').forEach(btn =>
            btn.addEventListener('click', (e) => {
                e.preventDefault();
                this.toggleSecretField(btn.dataset.target);
            })
        );

        const handlers = {
            startBtn: () => this.startStream(),
            stopBtn: () => this.stopStream(),
            saveConfigBtn: () => this.saveConfig(),
            clearLogsBtn: () => { this.logs = []; this.renderLogs(); },
            downloadLogsBtn: () => this.downloadLogs(),
            rtmpPort: () => this.updateRTMPUrl(),
            streamKey: () => this.updateRTMPUrl(),
            filterFfmpeg: () => this.renderLogs(),
            filterSrtla: () => this.renderLogs(),
            addSelectedIPs: () => this.network.addSelectedIPs(),
            refreshInterfaces: () => this.network.loadInterfaces(),
            loadFromFile: () => this.network.loadIPsFromFile(),
            saveToFile: () => this.network.saveIPsToFile(),
            refreshUSBNet: () => this.usbnet.load(),
            refreshModems: () => this.modem.load(),
            scanCamerasBtn: () => this.camera.startScan(),
            stopScanBtn: () => this.camera.stopScan(),
            scanUSBCamsBtn: () => this.usbcam.scan(),
            refreshWiFiNetworks: () => this.wifi.updateNetworks(),
            connectWiFiBtn: () => this.wifi.connect(),
            disconnectWiFiBtn: () => this.wifi.disconnect(),
            createHotspotBtn: () => this.wifi.createHotspot(),
            stopHotspotBtn: () => this.wifi.stopHotspot(),
            refreshReleasesBtn: () => this.updates.loadReleases()
        };

        Object.entries(handlers).forEach(([id, handler]) => {
            const el = document.getElementById(id);
            if (el) el.addEventListener('click', handler);
        });

        ['rtmpPort', 'streamKey'].forEach(id => {
            const el = document.getElementById(id);
            if (el) el.addEventListener('input', () => this.updateRTMPUrl());
        });

        // Debug mode toggle
        const debugToggle = document.getElementById('debugModeToggle');
        if (debugToggle) {
            debugToggle.addEventListener('change', (e) => {
                this.toggleDebugMode(e.target.checked);
            });
        }

        // Delegate camera button clicks
        document.addEventListener('click', (e) => {
            const t = e.target;
            if (t.classList.contains('btn-connect-camera') || t.classList.contains('btn-connect')) {
                const id = t.getAttribute('data-camera-id');
                if (id) this.camera.connect(id);
            }
            if (t.classList.contains('btn-preview-camera')) {
                const id = t.getAttribute('data-camera-id');
                if (id) this.camera.showPreviewModal(id);
            }
            if (t.classList.contains('btn-configure-camera') || t.classList.contains('btn-configure')) {
                const id = t.getAttribute('data-camera-id');
                if (id) this.camera.showConfigModal(id);
            }
            if (t.classList.contains('btn-forget-camera') || t.classList.contains('btn-forget')) {
                const id = t.getAttribute('data-camera-id');
                if (id && confirm('Remove this camera?')) this.camera.forget(id);
            }
            if (t.classList.contains('modem-ussd-btn') && !t.disabled) {
                const id = t.getAttribute('data-modem-id');
                if (id) this.modem.promptUSSD(id);
            }
            // USB camera buttons
            if (t.classList.contains('btn-usbcam-start')) {
                const id = t.getAttribute('data-usbcam-id');
                if (id) this.usbcam.showStartModal(id);
            }
            if (t.classList.contains('btn-usbcam-stop')) {
                const id = t.getAttribute('data-usbcam-id');
                if (id) this.usbcam.stopStreaming(id);
            }
            if (t.classList.contains('btn-usbcam-preview')) {
                const id = t.getAttribute('data-usbcam-id');
                if (id) this.usbcam.togglePreview(id);
            }
            if (t.classList.contains('btn-usbcam-fullscreen')) {
                const id = t.getAttribute('data-usbcam-id');
                if (id) this.usbcam.toggleFullscreen(id);
            }
        });
    }

    // Helper methods that delegate to managers
    quickConnectWiFi(ssid) {
        this.wifi.quickConnect(ssid);
    }

    deleteHotspot(index) {
        this.wifi.deleteHotspot(index);
    }
}

// Legacy global function support
window.copyCommand = function(elementId) {
    const el = document.getElementById(elementId);
    if (!el) return;
    copyToClipboard(el.textContent, (success) => {
        if (success) {
            const btn = el.parentElement?.querySelector('.btn-copy');
            if (btn) {
                const orig = btn.textContent;
                btn.textContent = 'Copied!';
                setTimeout(() => btn.textContent = orig, 2000);
            }
        }
    });
};

document.addEventListener('DOMContentLoaded', () => {
    window.manager = new SRTLAManager();
    window.updateManager = window.manager.updates;
});
