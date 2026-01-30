// WiFi Management
import { API } from './api.js';
import { escapeHtml, getWifiSignalBars, showNotification } from './utils.js';

export class WiFiManager {
    constructor() {
        this.wifiStatus = null;
        this.lastWifiCreds = null;
    }

    async load() {
        await this.updateStatus();
        await this.updateNetworks();
        await this.updateActiveHotspots();
    }

    async updateStatus() {
        try {
            const data = await API.get('/api/wifi/status');
            this.wifiStatus = data;
            const statusDiv = document.getElementById('wifiStatusContent');
            if (!statusDiv) return;
            
            if (data.connection?.connected) {
                const ssid = data.connection.ssid || 'Unknown';
                const ip = data.connection.ip || 'N/A';
                const signal = data.connection.signal;
                const iface = data.connection.interface || 'N/A';
                const isHotspot = signal === 0 || signal === undefined;
                
                statusDiv.innerHTML = isHotspot ? `
                    <div class="wifi-status-connected">
                        <p><strong>Hotspot Active:</strong> ${escapeHtml(ssid)}</p>
                        <p><strong>IP Address:</strong> ${escapeHtml(ip)}</p>
                        <p><strong>Interface:</strong> ${escapeHtml(iface)}</p>
                    </div>` : `
                    <div class="wifi-status-connected">
                        <p><strong>Connected to:</strong> ${escapeHtml(ssid)}</p>
                        <p><strong>IP Address:</strong> ${escapeHtml(ip)}</p>
                        <p><strong>Signal Strength:</strong> ${signal}%</p>
                        <p><strong>Interface:</strong> ${escapeHtml(iface)}</p>
                    </div>`;
            } else {
                statusDiv.innerHTML = '<p class="wifi-status-disconnected">Not connected to any WiFi network</p>';
            }
        } catch (e) {
            console.error('Failed to load WiFi status:', e);
            const statusDiv = document.getElementById('wifiStatusContent');
            if (statusDiv) statusDiv.innerHTML = '<p class="error">Failed to load WiFi status</p>';
        }
    }

    async updateNetworks() {
        try {
            const data = await API.get('/api/wifi/networks');
            const list = document.getElementById('wifiNetworksList');
            if (!list) return;
            
            if (!data.available) {
                list.innerHTML = '<p class="error">WiFi not available on this device</p>';
                return;
            }

            if (!data.networks || data.networks.length === 0) {
                list.innerHTML = '<p class="no-networks">No WiFi networks found. Try scanning again.</p>';
                return;
            }

            list.innerHTML = data.networks.map(net => {
                const bars = getWifiSignalBars(net.signal);
                const connected = net.connected ? 'wifi-network-connected' : '';
                return `
                    <div class="wifi-network-item ${connected}">
                        <div class="wifi-network-info">
                            <div class="wifi-network-name">${escapeHtml(net.ssid)}</div>
                            <div class="wifi-network-details">
                                <span class="wifi-signal">${bars}</span>
                                <span class="wifi-security">${net.security}</span>
                                ${net.dual_band ? '<span class="wifi-band-badge">5GHz</span>' : ''}
                                ${net.connected ? '<span class="wifi-connected-badge">Connected</span>' : ''}
                            </div>
                        </div>
                        <button class="btn-small" onclick="manager.quickConnectWiFi('${escapeHtml(net.ssid)}')">
                            ${net.connected ? 'Disconnect' : 'Connect'}
                        </button>
                    </div>
                `;
            }).join('');
        } catch (e) {
            console.error('Failed to load WiFi networks:', e);
            const list = document.getElementById('wifiNetworksList');
            if (list) list.innerHTML = '<p class="error">Failed to load WiFi networks</p>';
        }
    }

    async connect() {
        const ssidEl = document.getElementById('wifiSSID');
        const passwordEl = document.getElementById('wifiPassword');
        if (!ssidEl || !passwordEl) return;

        const ssid = ssidEl.value.trim();
        const password = passwordEl.value;

        if (!ssid) {
            showNotification('Please enter a network name', 'error');
            return;
        }

        try {
            const data = await API.post('/api/wifi/connect', { ssid, password });
            if (data.success) {
                this.lastWifiCreds = { ssid, password };
                showNotification(data.message, 'success');
                ssidEl.value = '';
                passwordEl.value = '';
                setTimeout(() => this.updateStatus(), 2000);
            } else {
                showNotification(data.message, 'error');
            }
        } catch (e) {
            console.error('Failed to connect:', e);
            showNotification('Failed to connect to WiFi', 'error');
        }
    }

    async quickConnect(ssid) {
        const status = await API.get('/api/wifi/status');
        
        if (status.connection?.connected && status.connection.ssid === ssid) {
            await this.disconnect();
        } else {
            const password = prompt(`Enter password for ${ssid}:`, '');
            if (password !== null) {
                try {
                    const data = await API.post('/api/wifi/connect', { ssid, password });
                    if (data.success) {
                        this.lastWifiCreds = { ssid, password };
                        showNotification(data.message, 'success');
                        setTimeout(() => this.updateStatus(), 2000);
                    } else {
                        showNotification(data.message, 'error');
                    }
                } catch (e) {
                    showNotification('Failed to connect to WiFi', 'error');
                }
            }
        }
    }

    async disconnect() {
        try {
            const data = await API.post('/api/wifi/disconnect');
            if (data.success) {
                showNotification(data.message, 'success');
                setTimeout(() => this.updateStatus(), 1000);
            } else {
                showNotification(data.message, 'error');
            }
        } catch (e) {
            showNotification('Failed to disconnect', 'error');
        }
    }

    async createHotspot() {
        const ssidEl = document.getElementById('hotspotSSID');
        const passwordEl = document.getElementById('hotspotPassword');
        const bandEl = document.getElementById('hotspotBand');
        if (!ssidEl || !passwordEl || !bandEl) return;

        const ssid = ssidEl.value.trim();
        const password = passwordEl.value.trim();
        const band = bandEl.value;

        if (!ssid || !password) {
            showNotification('Hotspot name and password are required', 'error');
            return;
        }

        if (password.length < 8) {
            showNotification('Password must be at least 8 characters', 'error');
            return;
        }

        try {
            const data = await API.post('/api/wifi/hotspot', { ssid, password, band, channel: 0 });
            if (data.success) {
                showNotification(data.message, 'success');
                ssidEl.value = '';
                passwordEl.value = '';
                setTimeout(() => {
                    this.updateStatus();
                    this.updateActiveHotspots();
                }, 2000);
            } else {
                showNotification(data.message, 'error');
            }
        } catch (e) {
            showNotification('Failed to create hotspot', 'error');
        }
    }

    async stopHotspot() {
        try {
            const data = await API.post('/api/wifi/hotspot/stop');
            if (data.success) {
                showNotification(data.message, 'success');
                setTimeout(() => {
                    this.updateStatus();
                    this.updateActiveHotspots();
                }, 1000);
            } else {
                showNotification(data.message, 'error');
            }
        } catch (e) {
            showNotification('Failed to stop hotspot', 'error');
        }
    }

    async updateActiveHotspots() {
        try {
            const data = await API.get('/api/wifi/hotspots');
            const list = document.getElementById('activeHotspotsList');
            if (!list) return;

            if (!data.hotspots || data.hotspots.length === 0) {
                list.innerHTML = '<p class="no-hotspots">No active hotspots</p>';
                return;
            }

            list.innerHTML = data.hotspots.map((hotspot, idx) => `
                <div class="hotspot-item">
                    <div class="hotspot-info">
                        <div class="hotspot-name">${escapeHtml(hotspot.ssid)}</div>
                        <div class="hotspot-details">
                            <span class="hotspot-detail-item">
                                <span class="hotspot-status-badge">Active</span>
                            </span>
                            ${hotspot.band ? `<span class="hotspot-detail-item">Band: ${hotspot.band} GHz</span>` : ''}
                            ${hotspot.interface ? `<span class="hotspot-detail-item">Interface: ${escapeHtml(hotspot.interface)}</span>` : ''}
                            ${hotspot.ip ? `<span class="hotspot-detail-item">IP: ${escapeHtml(hotspot.ip)}</span>` : ''}
                        </div>
                    </div>
                    <div class="hotspot-actions">
                        <button class="btn-delete-hotspot" onclick="manager.deleteHotspot(${idx})">Delete</button>
                    </div>
                </div>
            `).join('');
        } catch (e) {
            console.error('Failed to load hotspots:', e);
            const list = document.getElementById('activeHotspotsList');
            if (list) list.innerHTML = '<p class="no-hotspots">Unable to load hotspots</p>';
        }
    }

    async deleteHotspot(index) {
        if (!confirm('Are you sure you want to delete this hotspot?')) return;

        try {
            await API.delete(`/api/wifi/hotspots/${index}`);
            showNotification('Hotspot deleted', 'success');
            setTimeout(() => {
                this.updateStatus();
                this.updateActiveHotspots();
            }, 500);
        } catch (e) {
            console.error('Failed to delete hotspot:', e);
            showNotification('Failed to delete hotspot', 'error');
        }
    }
}
