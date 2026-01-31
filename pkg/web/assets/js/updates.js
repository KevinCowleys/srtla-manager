// Update Management Module
import { API } from './api.js';
import { showNotification } from './utils.js';

export class UpdateManager {
    constructor() {
        this.updateInfo = null;
        this.releases = [];
        this.checking = false;
        this.showAllReleases = false;
        this.showAllSRTLAReleases = false;
    }

    async checkForUpdates() {
        if (this.checking) return;
        
        this.checking = true;
        try {
            const response = await API.get('/api/updates/check');
            this.updateInfo = response;
            this.renderUpdateStatus();
            
            if (response.available) {
                showNotification('Update Available', `Version ${response.latest_version} is available!`, 'info');
            }
        } catch (error) {
            console.error('Failed to check for updates:', error);
            showNotification('Update Check Failed', error.message, 'error');
        } finally {
            this.checking = false;
        }
    }

    async loadReleases() {
        try {
            const response = await API.get('/api/updates/releases');
            this.releases = response;
            this.renderReleases();
        } catch (error) {
            console.error('Failed to load releases:', error);
            showNotification('Failed to Load Releases', error.message, 'error');
        }
    }

    async performUpdate(version) {
        if (!confirm(`Update to ${version}? The service will restart automatically.`)) {
            return;
        }

        try {
            showNotification('Update Started', `Updating to ${version}... The service will restart.`, 'info');
            const response = await API.post('/api/updates/perform', { version });
            showNotification('Update Complete', response.message || 'Update completed successfully. Service is restarting...', 'success');
            
            // Reload page after a delay to allow service to restart
            setTimeout(() => {
                window.location.reload();
            }, 5000);
        } catch (error) {
            console.error('Failed to perform update:', error);
            showNotification('Update Failed', error.message, 'error');
        }
    }

    renderUpdateStatus() {
        const container = document.getElementById('updateStatusContainer');
        if (!container) return;

        if (!this.updateInfo) {
            container.innerHTML = '<p class="loading">Checking for updates...</p>';
            return;
        }

        const { available, current_version, latest_version, is_prerelease, release_url, release_notes } = this.updateInfo;

        let statusHtml = `
            <div class="update-status-card">
                <div class="update-info">
                    <p><strong>Current Version:</strong> ${this.escapeHtml(current_version)}</p>
                    <p><strong>Latest Version:</strong> ${this.escapeHtml(latest_version)}${is_prerelease ? ' <span class="badge-prerelease">Pre-release</span>' : ''}</p>
                </div>
        `;

        if (available) {
            statusHtml += `
                <div class="update-available">
                    <p class="update-message">🎉 A new version is available!</p>
                    <button class="btn btn-primary" onclick="window.updateManager.performUpdate('${latest_version}')">Update Now</button>
                    <a href="${release_url}" target="_blank" class="btn btn-secondary">View Release</a>
                </div>
                <div class="release-notes">
                    <h4>Release Notes:</h4>
                    <div class="notes-content">${window.marked ? window.marked.parse(release_notes || '') : this.escapeHtml(release_notes).replace(/\n/g, '<br>')}</div>
                </div>
            `;
        } else {
            statusHtml += `<p class="update-current">You are running the latest version ✓</p>`;
        }

        statusHtml += `</div>`;
        container.innerHTML = statusHtml;
    }

    renderReleases() {
        const container = document.getElementById('versionsList');
        if (!container) return;

        if (this.releases.length === 0) {
            container.innerHTML = '<p class="no-releases">No releases found.</p>';
            return;
        }

        const displayReleases = this.showAllReleases ? this.releases : this.releases.slice(0, 3);
        let html = '<div class="releases-list">';
        
        for (const release of displayReleases) {
            const date = new Date(release.published_at).toLocaleDateString();
            const prereleaseBadge = release.prerelease ? '<span class="badge-prerelease">Pre-release</span>' : '';
            const draftBadge = release.draft ? '<span class="badge-draft">Draft</span>' : '';
            
            html += `
                <div class="release-item">
                    <div class="release-header">
                        <h3>${this.escapeHtml(release.tag_name)}</h3>
                        <span class="release-date">${date}</span>
                    </div>
                    <div class="release-badges">
                        ${prereleaseBadge}
                        ${draftBadge}
                    </div>
                    <p class="release-name">${this.escapeHtml(release.name || release.tag_name)}</p>
                    <div class="release-actions">
                        <button class="btn btn-small" onclick="window.updateManager.performUpdate('${release.tag_name}')">Update to This</button>
                        <a href="${release.html_url}" target="_blank" class="btn btn-small btn-secondary">Details</a>
                    </div>
                </div>
            `;
        }
        
        html += '</div>';
        
        if (this.releases.length > 3) {
            html += `
                <button class="btn btn-secondary view-more-btn" onclick="window.updateManager.toggleReleases()">
                    ${this.showAllReleases ? 'View Less' : `View More (${this.releases.length - 3} more)`}
                </button>
            `;
        }
        
        container.innerHTML = html;
    }

    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    async checkForSRTLASendUpdates() {
        try {
            const response = await API.get('/api/updates/srtla/check');
            this.srtlaSendUpdateInfo = response;
            this.renderSRTLASendUpdateStatus();
            
            if (response.available) {
                showNotification('SRTLA Send Update Available', `Version ${response.latest_version} is available!`, 'info');
            }
        } catch (error) {
            console.error('Failed to check for srtla_send updates:', error);
            document.getElementById('srtlaSendUpdateContainer').innerHTML = '<p class="error">Failed to check for updates</p>';
        }
    }

    async loadSRTLASendReleases() {
        try {
            const response = await API.get('/api/updates/srtla/releases');
            this.srtlaSendReleases = response;
            this.renderSRTLASendReleases();
        } catch (error) {
            console.error('Failed to load srtla_send releases:', error);
        }
    }

    async installSRTLASend(version) {
        if (!confirm(`Download and install srtla_send ${version}?`)) {
            return;
        }

        try {
            showNotification('Installing SRTLA Send', `Downloading and installing ${version}...`, 'info');
            const response = await API.post('/api/updates/srtla/install', { version });
            showNotification('Installation Started', response.message || 'Installation in progress...', 'success');
            
            // Refresh update status after installation
            setTimeout(() => {
                this.checkForSRTLASendUpdates();
            }, 3000);
        } catch (error) {
            console.error('Failed to install srtla_send:', error);
            showNotification('Installation Failed', error.message, 'error');
        }
    }

    renderSRTLASendUpdateStatus() {
        const container = document.getElementById('srtlaSendUpdateContainer');
        if (!container) return;

        if (!this.srtlaSendUpdateInfo) {
            container.innerHTML = '<p class="loading">Checking for SRTLA Send updates...</p>';
            return;
        }

        const { available, current_version, latest_version, is_prerelease, release_url, release_notes } = this.srtlaSendUpdateInfo;

        let statusHtml = `
            <div class="update-status-card srtla-update">
                <div class="update-info">
                    <p><strong>Current Version:</strong> ${this.escapeHtml(current_version)}</p>
                    <p><strong>Latest Version:</strong> ${this.escapeHtml(latest_version)}${is_prerelease ? ' <span class="badge-prerelease">Pre-release</span>' : ''}</p>
                </div>
        `;

        if (available) {
            statusHtml += `
                <div class="update-available">
                    <p class="update-message">🎉 A new SRTLA Send version is available!</p>
                    <button class="btn btn-primary" onclick="window.updateManager.installSRTLASend('${latest_version}')">Download & Install</button>
                    <a href="${release_url}" target="_blank" class="btn btn-secondary">View Release</a>
                </div>
                <div class="release-notes">
                    <h4>Release Notes:</h4>
                    <div class="notes-content">${window.marked ? window.marked.parse(release_notes || '') : this.escapeHtml(release_notes).replace(/\n/g, '<br>')}</div>
                </div>
            `;
        } else {
            statusHtml += `<p class="update-current">You are running the latest version ✓</p>`;
        }

        statusHtml += `</div>`;
        container.innerHTML = statusHtml;
    }

    renderSRTLASendReleases() {
        const container = document.getElementById('srtlaSendVersionsList');
        if (!container) return;

        if (!this.srtlaSendReleases || this.srtlaSendReleases.length === 0) {
            container.innerHTML = '<p class="no-releases">No releases found.</p>';
            return;
        }

        const displayReleases = this.showAllSRTLAReleases ? this.srtlaSendReleases : this.srtlaSendReleases.slice(0, 3);
        let html = '<div class="releases-list">';
        
        for (const release of displayReleases) {
            const date = new Date(release.published_at).toLocaleDateString();
            const prereleaseBadge = release.prerelease ? '<span class="badge-prerelease">Pre-release</span>' : '';
            const draftBadge = release.draft ? '<span class="badge-draft">Draft</span>' : '';
            
            html += `
                <div class="release-item">
                    <div class="release-header">
                        <h3>${this.escapeHtml(release.tag_name)}</h3>
                        <span class="release-date">${date}</span>
                    </div>
                    <div class="release-badges">
                        ${prereleaseBadge}
                        ${draftBadge}
                    </div>
                    <p class="release-name">${this.escapeHtml(release.name || release.tag_name)}</p>
                    <div class="release-actions">
                        <button class="btn btn-small" onclick="window.updateManager.installSRTLASend('${release.tag_name}')">Download & Install</button>
                        <a href="${release.html_url}" target="_blank" class="btn btn-small btn-secondary">View Details</a>
                    </div>
                </div>
            `;
        }
        
        html += '</div>';
        
        if (this.srtlaSendReleases.length > 3) {
            html += `
                <button class="btn btn-secondary view-more-btn" onclick="window.updateManager.toggleSRTLAReleases()">
                    ${this.showAllSRTLAReleases ? 'View Less' : `View More (${this.srtlaSendReleases.length - 3} more)`}
                </button>
            `;
        }
        
        container.innerHTML = html;
    }

    load() {
        this.checkForUpdates();
        this.loadReleases();
        this.checkForSRTLASendUpdates();
        this.loadSRTLASendReleases();
    }

    toggleReleases() {
        this.showAllReleases = !this.showAllReleases;
        this.renderReleases();
    }

    toggleSRTLAReleases() {
        this.showAllSRTLAReleases = !this.showAllSRTLAReleases;
        this.renderSRTLASendReleases();
    }
}
