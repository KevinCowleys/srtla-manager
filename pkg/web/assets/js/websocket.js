// WebSocket connection manager
export class WebSocketManager {
    constructor(onMessage) {
        this.ws = null;
        this.onMessage = onMessage;
        this.reconnectInterval = 3000;
    }

    connect() {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/ws`;
        
        this.ws = new WebSocket(wsUrl);
        
        this.ws.onopen = () => this.updateStatus(true);
        this.ws.onclose = () => {
            this.updateStatus(false);
            setTimeout(() => this.connect(), this.reconnectInterval);
        };
        this.ws.onerror = () => this.updateStatus(false);
        this.ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                this.onMessage(msg);
            } catch (e) {
                console.error('Failed to parse message:', e);
            }
        };
    }

    updateStatus(connected) {
        const el = document.getElementById('wsStatus');
        if (el) {
            el.textContent = connected ? 'Connected' : 'Disconnected';
            el.className = 'connection-status ' + (connected ? 'connected' : 'disconnected');
        }
    }
}
