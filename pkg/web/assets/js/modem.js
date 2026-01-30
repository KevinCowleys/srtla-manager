// LTE Modem Management
import { API } from './api.js';
import { escapeHtml, formatBytes, getSignalBars, showNotification } from './utils.js';

export class ModemManager {
    constructor() {}

    async load() {
        try {
            const data = await API.get('/api/modems');
            this.update(data);
        } catch (e) {
            console.error('Failed to load modems:', e);
            const grid = document.getElementById('modemGrid');
            if (grid) grid.innerHTML = '<div class="modem-error">Failed to load modem status</div>';
        }
    }

    update(data) {
        const grid = document.getElementById('modemGrid');
        if (!grid) return;

        if (!data.available) {
            grid.innerHTML = `
                <div class="modem-unavailable">
                    <p>ModemManager not detected</p>
                    <code>sudo apt install modemmanager</code>
                </div>`;
            return;
        }

        if (!data.modems || data.modems.length === 0) {
            grid.innerHTML = '<div class="modem-none">No LTE modems detected</div>';
            return;
        }

        grid.innerHTML = data.modems.map(m => this.renderCard(m)).join('');
    }

    renderCard(modem) {
        const signalBars = getSignalBars(modem.signal_percent);
        const stateClass = modem.state === 'connected' ? 'connected' :
                          modem.state === 'registered' ? 'registered' : 'disconnected';
        const stateIcon = modem.state === 'connected' ? '●' : '○';
        const dataTx = formatBytes(modem.data_tx);
        const dataRx = formatBytes(modem.data_rx);
        const isMmcli = (modem.id || '').startsWith('mmcli:');

        return `
            <div class="modem-card">
                <div class="modem-header">
                    <span class="modem-id">Modem ${modem.id}</span>
                    <span class="modem-model">${modem.manufacturer || ''} ${modem.model || ''}</span>
                </div>
                <div class="modem-signal">
                    <span class="signal-bars">${signalBars}</span>
                    <span class="signal-percent">${modem.signal_percent}%</span>
                </div>
                <div class="modem-carrier">
                    ${modem.carrier || 'Unknown'} ${modem.network_type ? `(${modem.network_type})` : ''}
                </div>
                <div class="modem-phone">${modem.phone_number || 'No number'}</div>
                <div class="modem-state ${stateClass}">
                    <span class="state-icon">${stateIcon}</span>
                    ${modem.state || 'Unknown'}
                </div>
                <div class="modem-data">
                    <span class="data-tx">↑${dataTx}</span>
                    <span class="data-rx">↓${dataRx}</span>
                </div>
                <div class="modem-interface">${modem.interface || ''} ${modem.ip_address ? `(${modem.ip_address})` : ''}</div>
                <div class="modem-actions">
                    <button class="btn-small modem-ussd-btn" data-modem-id="${escapeHtml(modem.id)}" ${!isMmcli ? 'disabled title="USSD only supported for mmcli devices"' : ''}>USSD</button>
                </div>
            </div>`;
    }

    promptUSSD(modemId) {
        const code = prompt('Enter USSD code (e.g. *123#):', '*123#');
        if (code === null) return;
        const trimmed = code.trim();
        if (!trimmed) {
            showNotification('USSD code is required', 'error');
            return;
        }
        this.sendUSSD(modemId, trimmed);
    }

    async sendUSSD(modemId, code) {
        try {
            const resp = await fetch(`/api/modems/${encodeURIComponent(modemId)}/ussd`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ code })
            });
            if (!resp.ok) {
                const err = await resp.text();
                showNotification(`USSD failed: ${err}`, 'error');
                return;
            }
            const data = await resp.json();
            const msg = data.response ? `USSD response: ${data.response}` : 'USSD sent (no response)';
            showNotification(msg);
        } catch (e) {
            console.error('USSD failed', e);
            showNotification('USSD failed to send', 'error');
        }
    }
}
