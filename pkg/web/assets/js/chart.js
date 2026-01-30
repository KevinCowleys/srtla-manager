// Chart management using Chart.js
export class ChartManager {
    constructor() {
        this.chart = null;
        this.chartData = [];
        this.maxDataPoints = 60;
    }

    init() {
        const ctx = document.getElementById('bitrateChart')?.getContext('2d');
        if (!ctx) return;

        this.chart = new Chart(ctx, {
            type: 'line',
            data: {
                labels: [],
                datasets: [{
                    label: 'FFmpeg (kbps)',
                    data: [],
                    borderColor: '#22c55e',
                    backgroundColor: 'rgba(34, 197, 94, 0.1)',
                    tension: 0.3,
                    fill: true,
                    borderWidth: 2
                }, {
                    label: 'SRTLA (Mbps)',
                    data: [],
                    borderColor: 'rgb(25, 131, 178)',
                    backgroundColor: 'rgba(25, 131, 178, 0.1)',
                    tension: 0.3,
                    fill: true,
                    borderWidth: 2
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                scales: {
                    x: { display: false },
                    y: {
                        beginAtZero: true,
                        grid: { color: 'rgba(39, 39, 42, 0.5)' },
                        ticks: { color: '#717171' }
                    }
                },
                plugins: {
                    legend: {
                        position: 'top',
                        labels: {
                            color: 'rgba(255, 255, 255, 0.87)',
                            usePointStyle: true,
                            padding: 20
                        }
                    }
                },
                animation: false
            }
        });
    }

    update(ffmpegBitrate, srtlaBitrate) {
        if (!this.chart) return;

        const now = new Date().toLocaleTimeString();
        this.chartData.push({
            time: now,
            ffmpeg: ffmpegBitrate || 0,
            srtla: srtlaBitrate || 0
        });

        if (this.chartData.length > this.maxDataPoints) {
            this.chartData.shift();
        }

        this.chart.data.labels = this.chartData.map(d => d.time);
        this.chart.data.datasets[0].data = this.chartData.map(d => d.ffmpeg);
        this.chart.data.datasets[1].data = this.chartData.map(d => d.srtla);
        this.chart.update('none');
    }
}
