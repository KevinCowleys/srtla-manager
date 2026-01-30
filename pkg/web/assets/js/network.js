// Network & Dependencies Management
import { API } from './api.js';
import { showNotification } from './utils.js';

export class NetworkManager {
    constructor() {}

    async loadDependencies() {
        try {
            const deps = await API.get('/api/system/dependencies');
            this.updateDepCard('ffmpegDep', 'ffmpegInstall', 'ffmpegCmd', deps.ffmpeg);
            this.updateDepCard('srtlaDep', 'srtlaInstall', 'srtlaCmd', deps.srtla);
            const allInstalled = deps.ffmpeg.installed && deps.srtla.installed;
            const depsSection = document.getElementById('depsSection');
            if (depsSection) depsSection.hidden = allInstalled;
        } catch (e) {
            console.error('Failed to load dependencies:', e);
        }
    }

    updateDepCard(cardId, installId, cmdId, dep) {
        const card = document.getElementById(cardId);
        if (!card) return;

        const icon = card.querySelector('.dep-icon');
        const status = card.querySelector('.dep-status');
        const installDiv = document.getElementById(installId);
        const cmdEl = document.getElementById(cmdId);

        if (dep.installed) {
            if (icon) {
                icon.textContent = '\u2713';
                icon.className = 'dep-icon installed';
            }
            if (status) {
                status.textContent = dep.version ? `v${dep.version}` : 'Installed';
                status.className = 'dep-status installed';
            }
            if (installDiv) installDiv.hidden = true;
        } else {
            if (icon) {
                icon.textContent = '\u2717';
                icon.className = 'dep-icon missing';
            }
            if (status) {
                status.textContent = 'Not installed';
                status.className = 'dep-status missing';
            }
            if (cmdEl) cmdEl.textContent = dep.install_command;
            if (installDiv) installDiv.hidden = false;
        }
    }

    async loadInterfaces() {
        try {
            const interfaces = await API.get('/api/system/interfaces');
            const container = document.getElementById('interfaceList');
            if (!container) return;

            if (interfaces.length === 0) {
                container.innerHTML = '<span class="no-interfaces">No network interfaces found</span>';
                return;
            }

            const sorted = interfaces.sort((a, b) => {
                const aIsUSB = /usb|u/.test(a.name);
                const bIsUSB = /usb|u/.test(b.name);
                if (aIsUSB && !bIsUSB) return -1;
                if (!aIsUSB && bIsUSB) return 1;
                return a.name.localeCompare(b.name);
            });

            container.innerHTML = sorted
                .filter(iface => !iface.is_loopback && iface.ips.length > 0)
                .map(iface => iface.ips.map(ip => {
                    const isUSB = /usb|u/.test(iface.name);
                    const badge = isUSB ? ' <span class="iface-badge">USB</span>' : '';
                    return `
                    <label class="interface-item ${iface.is_up ? 'up' : 'down'}">
                        <input type="checkbox" data-ip="${ip}">
                        <span class="iface-name">${iface.name}${badge}</span>
                        <span class="iface-ip">${ip}</span>
                        ${!iface.is_up ? '<span class="iface-down">(down)</span>' : ''}
                    </label>
                `}).join('')).join('');
        } catch (e) {
            console.error('Failed to load interfaces:', e);
            const container = document.getElementById('interfaceList');
            if (container) container.innerHTML = '<span class="error">Failed to load interfaces</span>';
        }
    }

    addSelectedIPs() {
        const checkboxes = document.querySelectorAll('#interfaceList input[type="checkbox"]:checked');
        const bindIPsEl = document.getElementById('bindIPs');
        if (!bindIPsEl) return;

        const currentIPs = bindIPsEl.value.split('\\n').map(s => s.trim()).filter(s => s);
        checkboxes.forEach(cb => {
            const ip = cb.dataset.ip;
            if (!currentIPs.includes(ip)) currentIPs.push(ip);
            cb.checked = false;
        });

        bindIPsEl.value = currentIPs.join('\\n');
        showNotification('IPs added');
    }

    async loadIPsFromFile() {
        const filePathEl = document.getElementById('ipsFilePath');
        if (!filePathEl) return;

        const filePath = filePathEl.value.trim();
        if (!filePath) {
            showNotification('Please enter a file path', 'error');
            return;
        }

        try {
            await API.put('/api/srtla/ips/file', { file_path: filePath });
            const data = await API.post('/api/srtla/ips/file/load');
            const bindIPsEl = document.getElementById('bindIPs');
            if (bindIPsEl) bindIPsEl.value = (data.ips || []).join('\\n');
            showNotification('IPs loaded from file');
        } catch (e) {
            console.error('Failed to load IPs:', e);
            showNotification(`Failed to load: ${e.message}`, 'error');
        }
    }

    async saveIPsToFile() {
        const filePathEl = document.getElementById('ipsFilePath');
        if (!filePathEl) return;

        const filePath = filePathEl.value.trim();
        if (!filePath) {
            showNotification('Please enter a file path', 'error');
            return;
        }

        try {
            await API.put('/api/srtla/ips/file', { file_path: filePath });
            await API.post('/api/srtla/ips/file/save');
            showNotification('IPs saved to file');
        } catch (e) {
            console.error('Failed to save IPs:', e);
            showNotification(`Failed to save: ${e.message}`, 'error');
        }
    }
}
