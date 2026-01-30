// USB Network Device Management
import { API } from './api.js';
import { escapeHtml } from './utils.js';

export class USBNetManager {
    constructor() {}

    async load() {
        try {
            const data = await API.get('/api/usbnet');
            this.update(data);
        } catch (e) {
            console.error('Failed to load usbnet:', e);
            const grid = document.getElementById('usbnetGrid');
            if (grid) grid.innerHTML = '<div class="usbnet-error">Failed to load USB network status</div>';
        }
    }

    update(data) {
        const grid = document.getElementById('usbnetGrid');
        if (!grid) return;

        if (!data.devices || data.devices.length === 0) {
            grid.innerHTML = '<div class="usbnet-none">No USB RNDIS devices detected</div>';
            return;
        }

        grid.innerHTML = data.devices.map(dev => this.renderCard(dev)).join('');
    }

    renderCard(device) {
        const stateClass = device.state === 'connected' ? 'connected' :
                          device.state === 'pending' ? 'pending' : 'disconnected';
        const errorClass = device.error ? 'has-error' : '';
        const errorMsg = device.error ? `<div class="device-error">${escapeHtml(device.error)}</div>` : '';
        const lastSeen = device.last_seen ? new Date(device.last_seen).toLocaleString() : 'Never';

        return `
            <div class="usbnet-card ${errorClass}">
                <div class="device-header">
                    <span class="device-serial">Serial: ${escapeHtml(device.serial || 'Unknown')}</span>
                    <span class="device-state ${stateClass}">${device.state || 'unknown'}</span>
                </div>
                <div class="device-details">
                    <div class="detail">
                        <span class="label">MAC</span>
                        <span class="value">${escapeHtml(device.mac || '-')}</span>
                    </div>
                    <div class="detail">
                        <span class="label">Interface</span>
                        <span class="value">${escapeHtml(device.interface || '-')}</span>
                    </div>
                    <div class="detail">
                        <span class="label">IPv4</span>
                        <span class="value">${escapeHtml(device.ipv4 || 'Pending DHCP')}</span>
                    </div>
                </div>
                ${errorMsg}
                <div class="device-timestamp">Last seen: ${lastSeen}</div>
            </div>`;
    }
}
