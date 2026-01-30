// Update Management Module
import { API } from './api.js';
import { showNotification } from './utils.js';

export class UpdateManager {
    constructor() {
        this.updateInfo = null;
        this.releases = [];
        this.checking = false;
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

    async downloadUpdate(version) {
        try {
            const response = await API.post('/api/updates/download', { version });
            if (response.download_url) {
                // Open download URL in a new tab
                window.open(response.download_url, '_blank');
                showNotification('Download Started', `Downloading ${version}...`, 'success');
            }
        } catch (error) {
            console.error('Failed to initiate download:', error);
            showNotification('Download Failed', error.message, 'error');
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
                    <button class="btn btn-primary" onclick="window.updateManager.downloadUpdate('${latest_version}')">Download Update</button>
                    <a href="${release_url}" target="_blank" class="btn btn-secondary">View Release</a>
                </div>
                <div class="release-notes">
                    <h4>Release Notes:</h4>
                    <div class="notes-content">${this.escapeHtml(release_notes).replace(/\n/g, '<br>')}</div>
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

        let html = '<div class="releases-list">';
        
        for (const release of this.releases) {
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
                        <button class="btn btn-small" onclick="window.updateManager.downloadUpdate('${release.tag_name}')">Download</button>
                        <a href="${release.html_url}" target="_blank" class="btn btn-small btn-secondary">Details</a>
                    </div>
                </div>
            `;
        }
        
        html += '</div>';
        container.innerHTML = html;
    }

    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    load() {
        this.checkForUpdates();
        this.loadReleases();
    }
}
