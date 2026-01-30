import { API } from './api.js';
import { showNotification } from './utils.js';

export class CameraManager {
    constructor() {
        this.cameras = [];
        this.previewPlayer = null;
        this.currentConfigCameraId = null;
        this.lastWifiCreds = null;
    }

    async load() {
        try {
            const data = await API.get('/api/cameras');
            this.cameras = data.cameras || [];
            this.renderDiscoveredCameras();
        } catch (e) {
            console.error('Failed to load cameras:', e);
        }
    }

    update(cameras) {
        this.cameras = cameras || [];
        this.renderDiscoveredCameras();
    }

    async startScan() {
        const scanBtn = document.getElementById('scanCamerasBtn');
        const stopScanBtn = document.getElementById('stopScanBtn');
        const statusEl = document.getElementById('scanStatus');

        scanBtn.disabled = true;
        stopScanBtn.disabled = false;
        statusEl.textContent = 'Scanning...';
        statusEl.classList.add('scanning');

        try {
            await API.post('/api/cameras/scan');

            // Poll for results
            let scanning = true;
            const pollInterval = setInterval(async () => {
                try {
                    const data = await API.get('/api/cameras');
                    this.renderDiscoveredCameras(data.cameras || []);

                    if (!data.scanning) {
                        scanning = false;
                        clearInterval(pollInterval);
                    }
                } catch (e) {
                    console.error('Polling error:', e);
                }
            }, 2000);

            // Auto-stop after 65 seconds (matching 60-second backend timeout + buffer)
            setTimeout(() => {
                if (scanning) {
                    this.stopScan();
                }
            }, 65000);
        } catch (e) {
            console.error('Scan error:', e);
            statusEl.textContent = 'Scan failed';
            statusEl.classList.remove('scanning');
            scanBtn.disabled = false;
            stopScanBtn.disabled = true;
        }
    }

    async stopScan() {
        const scanBtn = document.getElementById('scanCamerasBtn');
        const stopScanBtn = document.getElementById('stopScanBtn');
        const statusEl = document.getElementById('scanStatus');

        try {
            await API.post('/api/cameras/scan/stop');
        } catch (e) {
            console.error('Stop scan error:', e);
        }

        scanBtn.disabled = false;
        stopScanBtn.disabled = true;
        statusEl.textContent = '';
        statusEl.classList.remove('scanning');
    }

    renderDiscoveredCameras(cameras = this.cameras) {
        const container = document.getElementById('discoveredCamerasList');
        
        if (!cameras || cameras.length === 0) {
            container.innerHTML = '<p class="no-cameras">No cameras discovered. Click "Start Scanning" to begin.</p>';
            this.cameras = [];
            this.updatePreviewPlayer();
            return;
        }

        this.cameras = cameras;
        this.updatePreviewPlayer();

        container.innerHTML = cameras.map(camera => {
            const hasSavedConfig = !!camera.saved_config;
            const notDiscovered = camera.rssi < -90; // Low RSSI indicates saved but not discovered
            const savedBadge = hasSavedConfig ? '<span class="saved-config-badge">✓ Saved</span>' : '';
            const notFoundBadge = notDiscovered ? '<span class="not-found-badge">Not Found in Scan</span>' : '';
            
            return `
            <div class="camera-card ${notDiscovered ? 'not-discovered' : ''}">
                <div class="camera-card-header">
                    <div class="camera-card-title">
                        <div class="camera-name">${camera.name || 'Unknown Camera'} ${savedBadge} ${notFoundBadge}</div>
                        <div class="camera-model">${camera.model || 'Unknown'}</div>
                        <div class="camera-address">MAC: ${camera.id}</div>
                    </div>
                    ${!notDiscovered ? `
                    <div class="camera-signal">
                        <span>${camera.rssi} dBm</span>
                        <div class="signal-bars">
                            <div class="signal-bar ${Math.abs(camera.rssi) < 50 ? 'active' : ''}"></div>
                            <div class="signal-bar ${Math.abs(camera.rssi) < 60 ? 'active' : ''}"></div>
                            <div class="signal-bar ${Math.abs(camera.rssi) < 70 ? 'active' : ''}"></div>
                        </div>
                    </div>
                    ` : ''}
                </div>
                
                <div class="camera-status ${camera.state.toLowerCase()}">
                    ${camera.state.replace(/_/g, ' ').toUpperCase()}
                </div>

                <div class="camera-details">
                    ${!notDiscovered ? `
                        <div class="camera-detail-item">
                            <span class="camera-detail-label">Paired:</span>
                            <span class="camera-detail-value">${camera.paired ? 'Yes' : 'No'}</span>
                        </div>
                    ` : ''}
                    ${hasSavedConfig ? `
                        <div class="camera-detail-item">
                            <span class="camera-detail-label">Saved SSID:</span>
                            <span class="camera-detail-value">${camera.saved_config.wifi_ssid}</span>
                        </div>
                        <div class="camera-detail-item">
                            <span class="camera-detail-label">RTMP:</span>
                            <span class="camera-detail-value">${camera.saved_config.rtmp_url}</span>
                        </div>
                    ` : ''}
                </div>

                ${camera.last_error ? `
                    <div style="background: rgba(239, 68, 68, 0.1); padding: 8px; border-radius: 4px; font-size: 0.75rem; color: var(--error); margin-bottom: 12px;">
                        Error: ${camera.last_error}
                    </div>
                ` : ''}

                <div class="camera-actions">
                    <button class="btn-small btn-connect-camera" data-camera-id="${camera.id}">
                        ${notDiscovered && hasSavedConfig ? 'Quick Connect' : 'Connect'}
                    </button>
                    ${!notDiscovered ? `
                    <button class="btn-small btn-preview-camera" data-camera-id="${camera.id}">
                        Preview
                    </button>
                    ` : ''}
                    <button class="btn-small btn-configure-camera" data-camera-id="${camera.id}">
                        ${hasSavedConfig ? 'Stream' : 'Configure'}
                    </button>
                    <button class="btn-small btn-forget" data-camera-id="${camera.id}">
                        Forget
                    </button>
                </div>
            </div>
        `}).join('');
    }

    updatePreviewPlayer() {
        const videoEl = document.getElementById('cameraPreview');
        const statusEl = document.getElementById('cameraPreviewStatus');
        if (!videoEl || !statusEl) return;

        const streamingCam = this.cameras.find(c => (c.state || '').toLowerCase() === 'streaming');
        if (!streamingCam) {
            statusEl.textContent = 'No camera streaming';
            this.teardownPreview(videoEl);
            return;
        }

        statusEl.textContent = `Previewing ${streamingCam.name || streamingCam.id}`;
        const src = '/preview-temp/playlist.m3u8';

        if (window.Hls && window.Hls.isSupported()) {
            if (!this.previewPlayer) {
                this.previewPlayer = new Hls({
                    maxLiveSyncDuration: 3,
                    liveSyncDurationCount: 2,
                    liveBackBufferLength: 5,
                    maxBufferLength: 30,
                    maxMaxBufferLength: 60,
                    maxBufferSize: 60 * 1000 * 1000,
                    progressive: false,
                    lowLatencyMode: false
                });
                this.previewPlayer.on(Hls.Events.ERROR, (event, data) => {
                    console.warn('HLS error', data);
                    if (data.fatal) {
                        console.error('Fatal HLS error:', data);
                    }
                });
            }
            this.previewPlayer.loadSource(src);
            this.previewPlayer.attachMedia(videoEl);
        } else {
            // Native HLS (Safari)
            videoEl.src = src;
        }

        const play = videoEl.play();
        if (play && play.catch) {
            play.catch(() => {});
        }
    }

    teardownPreview(videoEl) {
        if (this.previewPlayer) {
            this.previewPlayer.destroy();
            this.previewPlayer = null;
        }
        if (videoEl) {
            videoEl.pause();
            videoEl.removeAttribute('src');
            videoEl.load();
        }
    }

    async connect(cameraId) {
        try {
            await API.post(`/api/cameras/${cameraId}/connect`);
            // Reload cameras after connection attempt
            setTimeout(() => this.load(), 1000);
        } catch (e) {
            console.error('Connection error:', e);
            alert('Failed to connect to camera: ' + e.message);
        }
    }

    async showConfigModal(cameraId) {
        const camera = this.cameras.find(c => c.id === cameraId);
        if (!camera) return;

        // Fetch defaults
        const defaults = await this.buildDefaultConfig(cameraId, '', '');
        
        // Check if modal exists, if not create it
        let modal = document.getElementById('cameraConfigModal');
        if (!modal) {
            modal = document.createElement('div');
            modal.id = 'cameraConfigModal';
            modal.className = 'modal';
            modal.innerHTML = `
                <div class="modal-content config-modal">
                    <div class="modal-header">
                        <h2>Configure Camera</h2>
                        <button class="modal-close" id="closeCameraConfigModal">&times;</button>
                    </div>
                    <div class="modal-body">
                        <form id="cameraConfigForm">
                            <div class="form-group">
                                <label for="configCameraName">Camera Name</label>
                                <input type="text" id="configCameraName" placeholder="My Camera" />
                            </div>
                            
                            <div class="form-section">
                                <h3>WiFi Settings</h3>
                                <div class="form-group">
                                    <label for="configWifiSSID">WiFi SSID <span class="required">*</span></label>
                                    <input type="text" id="configWifiSSID" placeholder="Your WiFi Network" required />
                                </div>
                                <div class="form-group">
                                    <label for="configWifiPassword">WiFi Password</label>
                                    <input type="password" id="configWifiPassword" placeholder="WiFi password (if any)" />
                                </div>
                            </div>

                            <div class="form-section">
                                <h3>RTMP Streaming</h3>
                                <div class="form-group">
                                    <label for="configRTMPURL">RTMP URL <span class="required">*</span></label>
                                    <input type="text" id="configRTMPURL" placeholder="rtmp://192.168.1.100:1935/live" required />
                                    <small>Target RTMP server for camera stream</small>
                                </div>
                            </div>

                            <div class="form-section">
                                <h3>Video Quality</h3>
                                <div class="form-row">
                                    <div class="form-group">
                                        <label for="configResolution">Resolution</label>
                                        <select id="configResolution">
                                            <option value="4k">4K (3840x2160)</option>
                                            <option value="2.7k">2.7K (2688x1512)</option>
                                            <option value="1080p" selected>1080p (1920x1080)</option>
                                            <option value="720p">720p (1280x720)</option>
                                        </select>
                                    </div>
                                    <div class="form-group">
                                        <label for="configFPS">Frame Rate (FPS)</label>
                                        <select id="configFPS">
                                            <option value="15">15 fps</option>
                                            <option value="24">24 fps</option>
                                            <option value="30" selected>30 fps</option>
                                            <option value="60">60 fps</option>
                                        </select>
                                    </div>
                                </div>
                                <div class="form-group">
                                    <label for="configBitrate">Bitrate (Kbps)</label>
                                    <input type="number" id="configBitrate" value="6000" min="500" max="20000" step="100" />
                                    <small>Higher bitrate = better quality but more bandwidth required</small>
                                </div>
                                <div class="form-group">
                                    <label for="configStabilization">Image Stabilization</label>
                                    <select id="configStabilization">
                                        <option value="off" selected>Off</option>
                                        <option value="standard">Standard</option>
                                        <option value="high">High</option>
                                        <option value="horizonsteady">Horizon Steady</option>
                                    </select>
                                </div>
                            </div>

                            <div class="form-actions">
                                <button type="button" class="btn btn-secondary" id="cancelConfigBtn">Cancel</button>
                                <button type="submit" class="btn btn-primary">Configure &amp; Stream</button>
                            </div>
                        </form>
                    </div>
                </div>
            `;
            document.body.appendChild(modal);

            // Event listeners
            document.getElementById('closeCameraConfigModal').addEventListener('click', () => {
                this.closeConfigModal();
            });
            
            document.getElementById('cancelConfigBtn').addEventListener('click', () => {
                this.closeConfigModal();
            });

            // Close on background click
            modal.addEventListener('click', (e) => {
                if (e.target === modal) {
                    this.closeConfigModal();
                }
            });

            // Form submission
            document.getElementById('cameraConfigForm').addEventListener('submit', (e) => {
                e.preventDefault();
                this.submitConfiguration();
            });
        }

        // Store current camera ID
        this.currentConfigCameraId = cameraId;

        // Populate form with defaults
        document.getElementById('configCameraName').value = camera.saved_config?.name || camera.name || '';
        document.getElementById('configWifiSSID').value = camera.saved_config?.wifi_ssid || defaults.wifi_ssid || '';
        document.getElementById('configWifiPassword').value = camera.saved_config?.wifi_password || defaults.wifi_password || '';
        document.getElementById('configRTMPURL').value = camera.saved_config?.rtmp_url || defaults.rtmp_url || '';
        document.getElementById('configResolution').value = '1080p';
        document.getElementById('configFPS').value = '30';
        document.getElementById('configBitrate').value = '6000';
        document.getElementById('configStabilization').value = 'off';

        // Show modal
        modal.style.display = 'flex';
    }

    closeConfigModal() {
        const modal = document.getElementById('cameraConfigModal');
        if (modal) {
            modal.style.display = 'none';
        }
    }

    async submitConfiguration() {
        const cameraId = this.currentConfigCameraId;
        if (!cameraId) return;

        // Get form values
        const config = {
            camera_name: document.getElementById('configCameraName').value,
            wifi_ssid: document.getElementById('configWifiSSID').value,
            wifi_password: document.getElementById('configWifiPassword').value,
            rtmp_url: document.getElementById('configRTMPURL').value,
            resolution: document.getElementById('configResolution').value,
            fps: parseInt(document.getElementById('configFPS').value),
            bitrate_kbps: parseInt(document.getElementById('configBitrate').value),
            stabilization: document.getElementById('configStabilization').value
        };

        // Validate
        if (!config.wifi_ssid || !config.rtmp_url) {
            showNotification('WiFi SSID and RTMP URL are required', 'error');
            return;
        }

        try {
            await API.post(`/api/cameras/${cameraId}/configure`, config);
            showNotification('Camera configuration started! The camera will begin streaming shortly.', 'success');
            this.closeConfigModal();
            
            // Reload cameras
            setTimeout(() => this.load(), 2000);
        } catch (e) {
            console.error('Configuration error:', e);
            showNotification('Failed to configure camera: ' + e.message, 'error');
        }
    }

    async buildDefaultConfig(cameraId, overrideSSID = '', overridePassword = '') {
        const port = document.getElementById('cameraPort')?.value || '1935';
        const key = document.getElementById('cameraStreamKey')?.value || 'live';
        let host = document.getElementById('cameraServerAddr')?.value;
        
        // If no host is set, try to detect it
        if (!host) {
            host = await this.detectDeviceIP();
        }
        
        let rtmp_url = host ? `rtmp://${host}:${port}/${key}` : '';
        let ssid = '';
        let wifi_password = '';
        let camera_name = '';

        // Load saved configuration for this camera (by MAC address)
        const camera = this.cameras.find(c => c.id === cameraId);
        if (camera?.saved_config) {
            ssid = camera.saved_config.wifi_ssid || '';
            wifi_password = camera.saved_config.wifi_password || '';
            rtmp_url = camera.saved_config.rtmp_url || rtmp_url;
            camera_name = camera.saved_config.name || camera.name || '';
        }

        // Apply overrides if provided
        if (overrideSSID) {
            ssid = overrideSSID;
        }
        if (overridePassword) {
            wifi_password = overridePassword;
        }

        // Fall back to cached if still empty
        if (!ssid) {
            ssid = this.lastWifiCreds?.ssid || '';
        }
        if (!wifi_password && this.lastWifiCreds?.password) {
            wifi_password = this.lastWifiCreds.password;
        }

        return {
            camera_name: camera_name || camera?.name || '',
            wifi_ssid: ssid,
            wifi_password,
            rtmp_url,
            resolution: '1080p',
            fps: 30,
            bitrate_kbps: 6000,
            stabilization: 'off'
        };
    }

    async showPreviewModal(cameraId) {
        const camera = this.cameras.find(c => c.id === cameraId);
        if (!camera) return;

        // Get WiFi SSID - either from saved config or prompt user
        let wifiSSID = '';
        let wifiPassword = '';

        if (camera.saved_config?.wifi_ssid) {
            wifiSSID = camera.saved_config.wifi_ssid;
            wifiPassword = camera.saved_config.wifi_password || '';
        } else {
            // Fetch fresh WiFi status
            try {
                const data = await API.get('/api/wifi/status');
                if (data.connection?.connected && data.connection?.ssid) {
                    wifiSSID = data.connection.ssid;
                }
            } catch (e) {
                console.warn('Failed to fetch WiFi status:', e);
            }

            // If still no SSID, prompt user
            if (!wifiSSID) {
                const userSSID = prompt('Enter WiFi SSID to preview camera:', '');
                if (userSSID === null) return; // User cancelled
                if (!userSSID) {
                    showNotification('WiFi SSID is required for preview', 'error');
                    return;
                }
                wifiSSID = userSSID;
                const userPass = prompt(`Enter password for "${userSSID}":`, '');
                if (userPass === null) return; // User cancelled
                wifiPassword = userPass;
            }
        }

        // Show preview status
        showNotification(`Starting preview for ${camera.name}...`, 'info');

        try {
            // Request preview stream from camera
            const data = await API.post(`/api/cameras/${cameraId}/preview`, {
                wifi_ssid: wifiSSID,
                wifi_password: wifiPassword
            });

            // Show preview in modal
            this.displayPreviewModal(camera.name, data.preview_url);
            
            // Auto-close after preview duration (e.g., 60 seconds)
            setTimeout(() => {
                this.closePreviewVideoModal();
                showNotification('Preview ended', 'info');
            }, 60000);

        } catch (e) {
            console.error('Preview error:', e);
            showNotification('Failed to start preview: ' + e.message, 'error');
        }
    }

    displayPreviewModal(cameraName, previewUrl) {
        // Check if modal exists, if not create it
        let modal = document.getElementById('cameraPreviewModal');
        if (!modal) {
            modal = document.createElement('div');
            modal.id = 'cameraPreviewModal';
            modal.className = 'modal';
            modal.innerHTML = `
                <div class="modal-content">
                    <div class="modal-header">
                        <h2>Camera Preview</h2>
                        <button class="modal-close" id="closePreviewModal">&times;</button>
                    </div>
                    <div class="modal-body">
                        <div id="previewVideoContainer" style="width: 100%; background: #000; border-radius: 4px; overflow: hidden;">
                            <video id="previewVideo" style="width: 100%; height: auto;" controls autoplay></video>
                        </div>
                        <p id="previewStatus" style="margin-top: 10px; font-size: 0.9rem; color: var(--text-secondary);">Loading preview...</p>
                    </div>
                </div>
            `;
            document.body.appendChild(modal);

            document.getElementById('closePreviewModal').addEventListener('click', () => {
                this.closePreviewVideoModal();
            });

            // Close on background click
            modal.addEventListener('click', (e) => {
                if (e.target === modal) {
                    this.closePreviewVideoModal();
                }
            });
        }

        const video = document.getElementById('previewVideo');
        const status = document.getElementById('previewStatus');
        
        // Display modal
        modal.style.display = 'flex';

        // Load HLS stream with retry logic
        const displayName = cameraName || 'Camera';
        status.textContent = `Waiting for ${displayName} to connect and stream...`;

        // Retry loading HLS every 2 seconds for up to 30 seconds
        let retryCount = 0;
        const maxRetries = 15;
        const retryInterval = setInterval(() => {
            retryCount++;
            
            if (retryCount > maxRetries) {
                clearInterval(retryInterval);
                status.textContent = 'Preview timeout - camera may not have connected to WiFi';
                status.style.color = 'var(--error)';
                return;
            }

            status.textContent = `Waiting for stream... (${retryCount}/${maxRetries})`;

            if (window.Hls && window.Hls.isSupported()) {
                if (this.previewPlayer) {
                    this.previewPlayer.destroy();
                }
                this.previewPlayer = new Hls({
                    maxLoadingDelay: 4,
                    maxBufferLength: 10,
                    manifestLoadingTimeOut: 2000,
                    manifestLoadingMaxRetry: 1,
                });
                
                this.previewPlayer.on(Hls.Events.MANIFEST_PARSED, () => {
                    clearInterval(retryInterval);
                    status.textContent = `Previewing ${displayName} (720p, 15fps, low bitrate)`;
                    status.style.color = '';
                    video.play().catch(() => {});
                });
                
                this.previewPlayer.on(Hls.Events.ERROR, (event, data) => {
                    if (data.fatal) {
                        console.warn('HLS error in preview (will retry):', data);
                    }
                });
                
                this.previewPlayer.loadSource(previewUrl);
                this.previewPlayer.attachMedia(video);
            } else {
                // Native HLS (Safari)
                video.src = previewUrl;
                video.play().then(() => {
                    clearInterval(retryInterval);
                    status.textContent = `Previewing ${displayName} (720p, 15fps, low bitrate)`;
                    status.style.color = '';
                }).catch(() => {});
            }
        }, 2000);
    }

    closePreviewVideoModal() {
        const modal = document.getElementById('cameraPreviewModal');
        if (modal) {
            modal.style.display = 'none';
        }
        if (this.previewPlayer) {
            this.previewPlayer.destroy();
            this.previewPlayer = null;
        }
    }

    async forget(cameraId) {
        try {
            await API.post(`/api/cameras/${cameraId}/forget`);
            // Refresh list
            this.load();
        } catch (e) {
            console.error('Forget camera error:', e);
            alert('Failed to remove camera: ' + e.message);
        }
    }

    async detectDeviceIP() {
        try {
            const data = await API.get('/api/system/ip');
            return data.ip || '';
        } catch (e) {
            console.error('Failed to detect IP:', e);
            return '';
        }
    }
}
