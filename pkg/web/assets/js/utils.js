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

export function showNotification(title, message, type = 'success') {
    // Handle old 2-parameter calls (message, type)
    if (typeof message === 'string' && (message === 'success' || message === 'error' || message === 'info')) {
        type = message;
        message = title;
        title = '';
    }
    
    const notification = document.createElement('div');
    notification.className = `notification ${type}`;
    
    if (title && message) {
        notification.innerHTML = `<strong>${escapeHtml(title)}</strong><br>${escapeHtml(message)}`;
    } else {
        notification.textContent = message || title;
    }
    
    document.body.appendChild(notification);
    
    // Stack notifications vertically
    const existingNotifications = document.querySelectorAll('.notification:not(.fade-out)');
    let offset = 24;
    existingNotifications.forEach(notif => {
        const height = notif.offsetHeight;
        notif.style.bottom = offset + 'px';
        offset += height + 12;
    });
    
    setTimeout(() => {
        notification.classList.add('fade-out');
        setTimeout(() => notification.remove(), 300);
    }, 3000);
}
