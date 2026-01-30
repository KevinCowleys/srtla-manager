import { API } from './api.js';
import { showNotification } from './utils.js';

export class USBCamManager {
    constructor() {
        this.cameras = [];
        this.scanning = false;
        this.previewPlayer = null;
        this.previewCameraId = null;
        this.previewMode = null; // 'api' (standalone preview) or 'inline' (during streaming)
        this.healthCheckInterval = null; // For MJPEG stream health monitoring
    }

    async load() {
        try {
            const data = await API.get('/api/usbcams');
            this.cameras = data.cameras || [];
            this.renderCameras();
        } catch (e) {
            console.error('Failed to load USB cameras:', e);
        }
    }

    async scan() {
        const scanBtn = document.getElementById('scanUSBCamsBtn');
        const statusEl = document.getElementById('usbcamScanStatus');

        if (scanBtn) scanBtn.disabled = true;
        if (statusEl) {
            statusEl.textContent = 'Scanning...';
            statusEl.classList.add('scanning');
        }
        this.scanning = true;

        try {
            const data = await API.post('/api/usbcams/scan');
            this.cameras = data.cameras || [];
            this.renderCameras();

            const count = this.cameras.length;
            showNotification(`Found ${count} USB camera${count !== 1 ? 's' : ''}`, 'success');
        } catch (e) {
            console.error('USB camera scan error:', e);
            showNotification('Failed to scan for USB cameras: ' + e.message, 'error');
        } finally {
            this.scanning = false;
            if (scanBtn) scanBtn.disabled = false;
            if (statusEl) {
                statusEl.textContent = '';
                statusEl.classList.remove('scanning');
            }
        }
    }

    renderCameras() {
        const container = document.getElementById('usbcamList');
        if (!container) return;

        if (!this.cameras || this.cameras.length === 0) {
            container.innerHTML = '<p class="no-cameras">No USB cameras detected. Click "Scan" to search.</p>';
            return;
        }

        container.innerHTML = this.cameras.map(cs => {
            const cam = cs.camera;
            const state = cs.state || 'idle';
            const isStreaming = state === 'streaming' || state === 'starting';

            // Build format info
            const formatSummary = this.buildFormatSummary(cam);

            return `
            <div class="usbcam-card ${isStreaming ? 'streaming' : ''}">
                <div class="usbcam-card-header">
                    <div class="usbcam-card-title">
                        <div class="usbcam-name">${cam.name || 'Unknown Camera'}
                            ${cam.is_elgato ? '<span class="usbcam-badge elgato">Elgato</span>' : ''}
                        </div>
                        <div class="usbcam-path">${cam.device_path} (${cam.id})</div>
                    </div>
                </div>

                <div class="camera-status ${state}">
                    ${state.toUpperCase()}
                </div>

                ${cs.last_error ? `
                    <div class="usbcam-error">
                        Error: ${cs.last_error}
                    </div>
                ` : ''}

                <div class="usbcam-formats">
                    ${formatSummary}
                </div>

                <div class="usbcam-actions">
                    ${isStreaming ? `
                        <button class="btn-small btn-usbcam-stop" data-usbcam-id="${cam.id}">
                            Stop Streaming
                        </button>
                    ` : `
                        <button class="btn-small btn-usbcam-start" data-usbcam-id="${cam.id}">
                            Start Streaming
                        </button>
                    `}
                    <button class="btn-small btn-usbcam-preview" data-usbcam-id="${cam.id}">
                        ${this.previewCameraId === cam.id ? 'Stop Preview' : 'Preview'}
                    </button>
                </div>

                ${this.previewCameraId === cam.id ? `
                <div class="usbcam-preview">
                    <div class="usbcam-preview-controls">
                        <button class="btn-small btn-usbcam-fullscreen" data-usbcam-id="${cam.id}" title="Fullscreen">
                            ⛶
                        </button>
                    </div>
                    <img class="usbcam-preview-mjpeg" id="usbcamPreview-${cam.id}"
                         style="width: 100%; max-width: 640px; border-radius: 8px;" />
                    <div class="usbcam-preview-status" id="usbcamPreviewStatus-${cam.id}">Loading preview...</div>
                </div>
                ` : ''}
            </div>
        `}).join('');

        // Re-initialize preview player if it was active (innerHTML destroyed the DOM)
        if (this.previewCameraId && this.previewMode === 'http-mjpeg') {
            const previewUrl = `/api/usbcams/${this.previewCameraId}/preview-stream`;
            requestAnimationFrame(() => this.initHTTPMJPEGPreview(this.previewCameraId, previewUrl));
        }
    }

    buildFormatSummary(cam) {
        if (!cam.formats || cam.formats.length === 0) {
            return '<span class="usbcam-no-formats">No formats detected</span>';
        }

        // Group by pixel format
        const byFormat = {};
        for (const f of cam.formats) {
            if (!byFormat[f.pixel_format]) {
                byFormat[f.pixel_format] = [];
            }
            byFormat[f.pixel_format].push(f);
        }

        const lines = [];
        for (const [fmt, formats] of Object.entries(byFormat)) {
            const resolutions = formats
                .sort((a, b) => (b.width * b.height) - (a.width * a.height))
                .slice(0, 3) // Show top 3 resolutions
                .map(f => `${f.width}x${f.height}`)
                .join(', ');
            lines.push(`<span class="usbcam-format-tag">${fmt}</span> ${resolutions}`);
        }

        return lines.join('<br>');
    }

    async showStartModal(cameraId) {
        const cs = this.cameras.find(c => c.camera.id === cameraId);
        if (!cs) return;

        const cam = cs.camera;

        // Build resolution options from camera formats
        const resolutions = this.getUniqueResolutions(cam);
        const encoders = this.getAvailableEncoders(cam);

        let modal = document.getElementById('usbcamStartModal');
        if (!modal) {
            modal = document.createElement('div');
            modal.id = 'usbcamStartModal';
            modal.className = 'modal';
            modal.innerHTML = `
                <div class="modal-content config-modal">
                    <div class="modal-header">
                        <h2>Start USB Camera Streaming</h2>
                        <button class="modal-close" id="closeUSBCamModal">&times;</button>
                    </div>
                    <div class="modal-body">
                        <form id="usbcamStartForm">
                            <div class="form-group">
                                <label>Camera</label>
                                <input type="text" id="usbcamModalName" readonly />
                            </div>

                            <div class="form-section">
                                <h3>Video Settings</h3>
                                <div class="form-row">
                                    <div class="form-group">
                                        <label for="usbcamResolution">Resolution</label>
                                        <select id="usbcamResolution"></select>
                                    </div>
                                    <div class="form-group">
                                        <label for="usbcamFPS">Frame Rate</label>
                                        <select id="usbcamFPS">
                                            <option value="15">15 fps</option>
                                            <option value="24">24 fps</option>
                                            <option value="30" selected>30 fps</option>
                                            <option value="60">60 fps</option>
                                        </select>
                                    </div>
                                </div>
                                <div class="form-group">
                                    <label for="usbcamBitrate">Bitrate (Kbps)</label>
                                    <input type="number" id="usbcamBitrate" value="6000" min="500" max="50000" step="100" />
                                    <small>Higher bitrate = better quality but more bandwidth</small>
                                </div>
                            </div>

                            <div class="form-section">
                                <h3>Encoder</h3>
                                <div class="form-group">
                                    <label for="usbcamEncoder">Video Encoder</label>
                                    <select id="usbcamEncoder"></select>
                                    <small id="usbcamEncoderHelp"></small>
                                </div>
                            </div>

                            <div class="form-actions">
                                <button type="button" class="btn btn-secondary" id="cancelUSBCamBtn">Cancel</button>
                                <button type="submit" class="btn btn-primary">Start Streaming</button>
                            </div>
                        </form>
                    </div>
                </div>
            `;
            document.body.appendChild(modal);

            document.getElementById('closeUSBCamModal').addEventListener('click', () => this.closeStartModal());
            document.getElementById('cancelUSBCamBtn').addEventListener('click', () => this.closeStartModal());
            modal.addEventListener('click', (e) => {
                if (e.target === modal) this.closeStartModal();
            });
            document.getElementById('usbcamStartForm').addEventListener('submit', (e) => {
                e.preventDefault();
                this.submitStart();
            });
            document.getElementById('usbcamEncoder').addEventListener('change', () => this.updateEncoderHelp());
        }

        // Store camera ID
        modal.dataset.cameraId = cameraId;

        // Populate camera name
        document.getElementById('usbcamModalName').value = cam.name || cam.id;

        // Populate resolution options
        const resSelect = document.getElementById('usbcamResolution');
        resSelect.innerHTML = resolutions.map(r =>
            `<option value="${r.width}x${r.height}" ${r.width === 1920 && r.height === 1080 ? 'selected' : ''}>${r.width}x${r.height}</option>`
        ).join('');

        // If no 1920x1080, select first
        if (!resolutions.some(r => r.width === 1920 && r.height === 1080) && resolutions.length > 0) {
            resSelect.selectedIndex = 0;
        }

        // Populate encoder options
        const encSelect = document.getElementById('usbcamEncoder');
        encSelect.innerHTML = encoders.map(e =>
            `<option value="${e.value}" ${e.value === 'libx264' ? 'selected' : ''}>${e.label}</option>`
        ).join('');

        this.updateEncoderHelp();

        // Show modal
        modal.style.display = 'flex';
    }

    closeStartModal() {
        const modal = document.getElementById('usbcamStartModal');
        if (modal) modal.style.display = 'none';
    }

    updateEncoderHelp() {
        const encoder = document.getElementById('usbcamEncoder')?.value;
        const help = document.getElementById('usbcamEncoderHelp');
        if (!help) return;

        const descriptions = {
            'libx264': 'Software encoding - works everywhere, uses CPU. Good default choice.',
            'h264_vaapi': 'Intel/AMD GPU hardware encoding - low CPU usage if supported.',
            'h264_nvenc': 'NVIDIA GPU hardware encoding - low CPU usage, requires NVIDIA GPU.',
            'copy': 'Passthrough - no re-encoding. Only works if camera outputs H.264 (e.g., Elgato Cam Link).'
        };

        help.textContent = descriptions[encoder] || '';
    }

    getUniqueResolutions(cam) {
        if (!cam.formats || cam.formats.length === 0) {
            return [
                { width: 1920, height: 1080 },
                { width: 1280, height: 720 },
                { width: 640, height: 480 }
            ];
        }

        const seen = new Set();
        const resolutions = [];
        for (const f of cam.formats) {
            const key = `${f.width}x${f.height}`;
            if (!seen.has(key)) {
                seen.add(key);
                resolutions.push({ width: f.width, height: f.height });
            }
        }

        // Sort by resolution descending
        resolutions.sort((a, b) => (b.width * b.height) - (a.width * a.height));
        return resolutions;
    }

    getAvailableEncoders(cam) {
        const encoders = [
            { value: 'libx264', label: 'Software (libx264)' },
            { value: 'h264_vaapi', label: 'VAAPI (Intel/AMD GPU)' },
            { value: 'h264_nvenc', label: 'NVENC (NVIDIA GPU)' }
        ];

        // Add copy option if camera supports H.264
        if (cam.formats && cam.formats.some(f => f.pixel_format === 'H264')) {
            encoders.push({ value: 'copy', label: 'Passthrough (H.264 copy)' });
        }

        return encoders;
    }

    async submitStart() {
        const modal = document.getElementById('usbcamStartModal');
        if (!modal) return;

        const cameraId = modal.dataset.cameraId;
        if (!cameraId) return;

        // Stop preview if running (backend will take over FFmpeg)
        if (this.previewCameraId) {
            this.teardownPlayer();
            if (this.previewMode === 'api') {
                try { await API.post(`/api/usbcams/${this.previewCameraId}/preview/stop`); } catch (e) { /* ignore */ }
            }
            this.previewCameraId = null;
            this.previewMode = null;
        }

        const resValue = document.getElementById('usbcamResolution').value;
        const [width, height] = resValue.split('x').map(Number);

        const config = {
            width: width,
            height: height,
            fps: parseInt(document.getElementById('usbcamFPS').value),
            bitrate: parseInt(document.getElementById('usbcamBitrate').value),
            encoder: document.getElementById('usbcamEncoder').value
        };

        try {
            await API.post(`/api/usbcams/${cameraId}/start`, config);
            showNotification('USB camera streaming started!', 'success');
            this.closeStartModal();
            // Reload after a short delay
            setTimeout(() => this.load(), 1000);
        } catch (e) {
            console.error('Start streaming error:', e);
            showNotification('Failed to start streaming: ' + e.message, 'error');
        }
    }

    async togglePreview(cameraId) {
        // If already previewing this camera, hide it
        if (this.previewCameraId === cameraId) {
            await this.stopPreview();
            return;
        }

        // Stop any existing preview first
        await this.stopPreview();

        // Show preview settings modal
        this.showPreviewSettingsModal(cameraId);
    }

    showPreviewSettingsModal(cameraId) {
        const cs = this.cameras.find(c => c.camera.id === cameraId);
        if (!cs) return;

        const cam = cs.camera;

        // Build supported combos from actual camera formats
        const combos = [];
        const seen = new Set();

        if (cam.formats && cam.formats.length > 0) {
            // Extract actual resolution+fps combinations from camera
            for (const fmt of cam.formats) {
                if (!fmt.fps || fmt.fps.length === 0) continue;
                
                for (const fps of fmt.fps) {
                    const key = `${fmt.width}x${fmt.height}@${fps}`;
                    if (!seen.has(key)) {
                        seen.add(key);
                        combos.push({
                            label: `${fmt.width}x${fmt.height} @ ${fps}fps`,
                            width: fmt.width,
                            height: fmt.height,
                            fps: fps
                        });
                    }
                }
            }
            
            // Sort combos: by resolution descending, then by fps descending
            combos.sort((a, b) => {
                const resA = a.width * a.height;
                const resB = b.width * b.height;
                if (resA !== resB) return resB - resA;
                return b.fps - a.fps;
            });
        } else {
            // Fallback if no formats detected
            const resolutions = this.getUniqueResolutions(cam);
            const fpsOptions = [15, 24, 30, 60];
            for (const res of resolutions) {
                for (const fps of fpsOptions) {
                    combos.push({
                        label: `${res.width}x${res.height} @ ${fps}fps`,
                        width: res.width,
                        height: res.height,
                        fps: fps
                    });
                }
            }
        }

        let modal = document.getElementById('usbcamPreviewSettingsModal');
        if (!modal) {
            modal = document.createElement('div');
            modal.id = 'usbcamPreviewSettingsModal';
            modal.className = 'modal';
            modal.innerHTML = `
                <div class="modal-content config-modal">
                    <div class="modal-header">
                        <h2>Preview Settings</h2>
                        <button class="modal-close" id="closePreviewSettingsModal">&times;</button>
                    </div>
                    <div class="modal-body">
                        <form id="usbcamPreviewSettingsForm">
                            <div class="form-group">
                                <label>Camera</label>
                                <input type="text" id="previewModalName" readonly />
                            </div>

                            <div class="form-section">
                                <h3>Preset Configurations</h3>
                                <div class="form-group">
                                    <label for="previewCombo">Supported Resolution + FPS</label>
                                    <select id="previewCombo"></select>
                                    <small>Select a combination known to work with your camera</small>
                                </div>
                            </div>

                            <div class="form-section">
                                <h3>Quality Settings</h3>
                                <div class="form-group">
                                    <label for="previewBitrate">Bitrate (Kbps)</label>
                                    <input type="number" id="previewBitrate" value="3000" min="500" max="50000" step="100" />
                                    <small>MJPEG quality: higher = better quality but more bandwidth</small>
                                </div>
                            </div>

                            <div class="form-section">
                                <label>
                                    <input type="checkbox" id="previewAdvanced" />
                                    Advanced: Override resolution & FPS
                                </label>
                            </div>

                            <div id="previewAdvancedSettings" style="display: none;" class="form-section">
                                <div class="form-row">
                                    <div class="form-group">
                                        <label for="previewWidth">Width</label>
                                        <input type="number" id="previewWidth" value="1280" min="320" max="4096" disabled />
                                    </div>
                                    <div class="form-group">
                                        <label for="previewHeight">Height</label>
                                        <input type="number" id="previewHeight" value="720" min="240" max="2160" disabled />
                                    </div>
                                </div>
                                <div class="form-group">
                                    <label for="previewFPSOverride">Frame Rate (fps)</label>
                                    <input type="number" id="previewFPSOverride" value="30" min="1" max="120" disabled />
                                </div>
                                <small style="color: var(--color-text-secondary);">Use at your own risk - unsupported combinations may not work</small>
                            </div>

                            <div class="form-actions">
                                <button type="button" class="btn btn-secondary" id="cancelPreviewBtn">Cancel</button>
                                <button type="submit" class="btn btn-primary">Start Preview</button>
                            </div>
                        </form>
                    </div>
                </div>
            `;
            document.body.appendChild(modal);

            document.getElementById('closePreviewSettingsModal').addEventListener('click', () => this.closePreviewSettingsModal());
            document.getElementById('cancelPreviewBtn').addEventListener('click', () => this.closePreviewSettingsModal());
            document.getElementById('previewAdvanced').addEventListener('change', (e) => {
                const advSettings = document.getElementById('previewAdvancedSettings');
                const isAdvanced = e.target.checked;
                advSettings.style.display = isAdvanced ? 'block' : 'none';
                
                // Disable/enable inputs to prevent validation errors on hidden fields
                document.getElementById('previewWidth').disabled = !isAdvanced;
                document.getElementById('previewHeight').disabled = !isAdvanced;
                document.getElementById('previewFPSOverride').disabled = !isAdvanced;
            });
            modal.addEventListener('click', (e) => {
                if (e.target === modal) this.closePreviewSettingsModal();
            });
            document.getElementById('usbcamPreviewSettingsForm').addEventListener('submit', (e) => {
                e.preventDefault();
                this.startPreviewWithSettings();
            });
        }

        // Store camera ID
        modal.dataset.cameraId = cameraId;

        // Populate camera name
        document.getElementById('previewModalName').value = cam.name || cam.id;

        // Populate combo options
        const comboSelect = document.getElementById('previewCombo');
        comboSelect.innerHTML = combos.map((combo, idx) =>
            `<option value="${idx}" ${combo.width === 1280 && combo.height === 720 && combo.fps === 30 ? 'selected' : ''}>${combo.label}</option>`
        ).join('');

        // Store combos for easy access
        modal.dataset.combos = JSON.stringify(combos);

        // Update advanced fields when combo changes
        if (!comboSelect._listenerAttached) {
            comboSelect.addEventListener('change', (e) => {
                const combos = JSON.parse(modal.dataset.combos);
                const combo = combos[parseInt(e.target.value)];
                document.getElementById('previewWidth').value = combo.width;
                document.getElementById('previewHeight').value = combo.height;
                document.getElementById('previewFPSOverride').value = combo.fps;
            });
            comboSelect._listenerAttached = true;
        }

        // Show modal
        modal.style.display = 'flex';
    }

    async startPreviewWithSettings() {
        const modal = document.getElementById('usbcamPreviewSettingsModal');
        if (!modal) return;

        const cameraId = modal.dataset.cameraId;
        if (!cameraId) return;

        let width, height, fps;

        // Get values from advanced override or preset combo
        const useAdvanced = document.getElementById('previewAdvanced').checked;
        if (useAdvanced) {
            width = parseInt(document.getElementById('previewWidth').value);
            height = parseInt(document.getElementById('previewHeight').value);
            fps = parseInt(document.getElementById('previewFPSOverride').value);
        } else {
            const combos = JSON.parse(modal.dataset.combos);
            const comboIdx = parseInt(document.getElementById('previewCombo').value);
            const combo = combos[comboIdx];
            width = combo.width;
            height = combo.height;
            fps = combo.fps;
        }

        const config = {
            width: width,
            height: height,
            fps: fps,
            bitrate: parseInt(document.getElementById('previewBitrate').value)
        };

        try {
            const data = await API.post(`/api/usbcams/${cameraId}/preview`, config);
            this.previewMode = 'http-mjpeg';
            this.previewCameraId = cameraId;
            this.previewConfig = config;
            this.closePreviewSettingsModal();
            this.renderCameras();
            requestAnimationFrame(() => this.initHTTPMJPEGPreview(cameraId, data.preview_url));
        } catch (e) {
            console.error('Preview start error:', e);
            showNotification('Failed to start preview: ' + e.message, 'error');
        }
    }

    closePreviewSettingsModal() {
        const modal = document.getElementById('usbcamPreviewSettingsModal');
        if (modal) modal.style.display = 'none';
    }

    async stopPreview() {
        this.teardownPlayer();

        // If we started preview via API, tell the backend to stop
        if ((this.previewMode === 'api' || this.previewMode === 'http-mjpeg') && this.previewCameraId) {
            try {
                await API.post(`/api/usbcams/${this.previewCameraId}/preview/stop`);
            } catch (e) {
                console.error('Preview stop error:', e);
            }
        }

        this.previewCameraId = null;
        this.previewMode = null;
        this.renderCameras();
    }

    initPreviewPlayer(cameraId, hlsUrl) {
        const videoEl = document.getElementById(`usbcamPreview-${cameraId}`);
        const statusEl = document.getElementById(`usbcamPreviewStatus-${cameraId}`);
        if (!videoEl) return;

        if (window.Hls && window.Hls.isSupported()) {
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

            this.previewPlayer.on(Hls.Events.MANIFEST_PARSED, () => {
                if (statusEl) statusEl.textContent = 'Live preview';
                videoEl.play().catch(() => {});
            });

            this.previewPlayer.on(Hls.Events.ERROR, (event, data) => {
                console.warn('USB cam HLS error:', data);
                if (data.fatal) {
                    if (statusEl) statusEl.textContent = 'Starting preview...';
                    // Retry after a delay (HLS segments may not be ready yet)
                    setTimeout(() => {
                        if (this.previewPlayer && this.previewCameraId === cameraId) {
                            this.previewPlayer.loadSource(hlsUrl);
                        }
                    }, 3000);
                }
            });

            this.previewPlayer.loadSource(hlsUrl);
            this.previewPlayer.attachMedia(videoEl);
        } else {
            // Native HLS support (Safari)
            videoEl.src = hlsUrl;
            videoEl.addEventListener('loadeddata', () => {
                if (statusEl) statusEl.textContent = 'Live preview';
            }, { once: true });
        }

        videoEl.play().catch(() => {});
    }

    initHTTPMJPEGPreview(cameraId, previewUrl) {
        const imgEl = document.getElementById(`usbcamPreview-${cameraId}`);
        const statusEl = document.getElementById(`usbcamPreviewStatus-${cameraId}`);
        if (!imgEl) return;

        let reconnectAttempts = 0;
        const maxReconnects = 10; // Match backend retry limit
        let lastCheckTime = Date.now();
        let isStreamHealthy = false;
        
        const clearHealthCheck = () => {
            if (this.healthCheckInterval) {
                clearInterval(this.healthCheckInterval);
                this.healthCheckInterval = null;
            }
        };
        
        const startStream = () => {
            // Add timestamp to prevent caching
            const streamUrl = `${previewUrl}?t=${Date.now()}`;
            imgEl.src = streamUrl;
            lastCheckTime = Date.now();
            isStreamHealthy = false;
        };
        
        const reconnect = () => {
            if (reconnectAttempts >= maxReconnects || this.previewCameraId !== cameraId) {
                if (statusEl) statusEl.textContent = 'Connection lost';
                clearHealthCheck();
                return;
            }
            
            reconnectAttempts++;
            if (statusEl) statusEl.textContent = `Reconnecting... (${reconnectAttempts}/${maxReconnects})`;
            
            // Exponential backoff: 1s, 2s, 4s, 8s... capped at 30s
            const backoff = Math.min(Math.pow(2, reconnectAttempts - 1) * 1000, 30000);
            setTimeout(() => {
                if (this.previewCameraId === cameraId) {
                    startStream();
                }
            }, backoff);
        };
        
        imgEl.onload = () => {
            if (statusEl) statusEl.textContent = 'Live preview';
            isStreamHealthy = true;
            reconnectAttempts = 0; // Reset on successful load
            
            // Clear any existing health check
            clearHealthCheck();
            
            // Start monitoring stream health
            // MJPEG streams can fail mid-stream with ERR_INCOMPLETE_CHUNKED_ENCODING
            // We detect this by checking if the naturalWidth becomes 0 (stream broken)
            this.healthCheckInterval = setInterval(() => {
                if (this.previewCameraId !== cameraId) {
                    clearHealthCheck();
                    return;
                }
                
                // Check if image element still has valid dimensions
                // If FFmpeg crashes, the browser will set naturalWidth to 0
                if (imgEl.naturalWidth === 0 || imgEl.naturalHeight === 0) {
                    console.log(`[Preview] Stream health check failed for ${cameraId}, reloading...`);
                    clearHealthCheck();
                    isStreamHealthy = false;
                    reconnect();
                } else {
                    // Stream is healthy, reset retry count if streaming for >30s
                    const streamDuration = Date.now() - lastCheckTime;
                    if (streamDuration > 30000 && reconnectAttempts > 0) {
                        console.log(`[Preview] Stream healthy for ${cameraId}, resetting retry count`);
                        reconnectAttempts = 0;
                        lastCheckTime = Date.now();
                    }
                }
            }, 5000); // Check every 5 seconds
        };
        
        imgEl.onerror = () => {
            clearHealthCheck();
            reconnect();
        };
        
        // Store cleanup function for teardown
        imgEl.dataset.cleanupHealthCheck = 'true';
        
        startStream();
    }

    teardownPlayer() {
        // Clear health check interval
        if (this.healthCheckInterval) {
            clearInterval(this.healthCheckInterval);
            this.healthCheckInterval = null;
        }
        
        if (this.previewPlayer) {
            this.previewPlayer.destroy();
            this.previewPlayer = null;
        }
        if (this.previewCameraId) {
            const videoEl = document.getElementById(`usbcamPreview-${this.previewCameraId}`);
            if (videoEl) {
                // Handle video element (HLS mode)
                if (videoEl.tagName === 'VIDEO' && typeof videoEl.pause === 'function') {
                    videoEl.pause();
                    videoEl.removeAttribute('src');
                    videoEl.load();
                }
                // Handle img element (HTTP MJPEG mode)
                else if (videoEl.tagName === 'IMG') {
                    // Clear event handlers to stop auto-reconnect
                    videoEl.onload = null;
                    videoEl.onerror = null;
                    // Use empty data URL to stop the stream
                    videoEl.src = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7';
                }
            }
        }
    }

    async stopStreaming(cameraId) {
        // Stop preview if active for this camera
        if (this.previewCameraId === cameraId) {
            this.teardownPlayer();
            this.previewCameraId = null;
            this.previewMode = null;
        }

        try {
            await API.post(`/api/usbcams/${cameraId}/stop`);
            showNotification('USB camera streaming stopped', 'success');
            setTimeout(() => this.load(), 500);
        } catch (e) {
            console.error('Stop streaming error:', e);
            showNotification('Failed to stop streaming: ' + e.message, 'error');
        }
    }

    toggleFullscreen(cameraId) {
        const previewEl = document.getElementById(`usbcamPreview-${cameraId}`);
        if (!previewEl) return;

        if (!document.fullscreenElement) {
            // Enter fullscreen
            if (previewEl.requestFullscreen) {
                previewEl.requestFullscreen();
            } else if (previewEl.webkitRequestFullscreen) {
                previewEl.webkitRequestFullscreen();
            } else if (previewEl.msRequestFullscreen) {
                previewEl.msRequestFullscreen();
            }
        } else {
            // Exit fullscreen
            if (document.exitFullscreen) {
                document.exitFullscreen();
            } else if (document.webkitExitFullscreen) {
                document.webkitExitFullscreen();
            } else if (document.msExitFullscreen) {
                document.msExitFullscreen();
            }
        }
    }
}
