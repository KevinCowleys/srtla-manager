export function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

export function formatBytes(bytes) {
    if (!bytes || bytes === 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + units[i];
}

export function getSignalBars(percent) {
    const bars = Math.ceil((percent || 0) / 25);
    let result = '';
    for (let i = 1; i <= 4; i++) {
        result += i <= bars ? '▂▄▆█'[i-1] : '░';
    }
    return result;
}

export function getWifiSignalBars(signal) {
    if (signal >= 70) return '████ Excellent';
    if (signal >= 50) return '███ Good';
    if (signal >= 30) return '██ Fair';
    return '█ Weak';
}

export function copyToClipboard(text, callback) {
    navigator.clipboard.writeText(text).then(() => {
        if (callback) callback(true);
    }).catch(err => {
        console.error('Failed to copy:', err);
        if (callback) callback(false);
    });
}

export function showNotification(message, type = 'success') {
    const notification = document.createElement('div');
    notification.className = `notification ${type}`;
    notification.textContent = message;
    document.body.appendChild(notification);
    setTimeout(() => {
        notification.classList.add('fade-out');
        setTimeout(() => notification.remove(), 300);
    }, 3000);
}
