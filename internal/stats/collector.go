package stats

import (
	"sync"
	"time"
)

const (
	HistorySize     = 300 // 5 minutes at 1 second intervals
	HistoryInterval = time.Second
)

type DataPoint struct {
	Timestamp     time.Time `json:"timestamp"`
	FFmpegBitrate float64   `json:"ffmpeg_bitrate"`
	SRTLABitrate  float64   `json:"srtla_bitrate"`
	FPS           float64   `json:"fps"`
}

type Collector struct {
	mu      sync.RWMutex
	history []DataPoint
	pos     int
	full    bool
}

func NewCollector() *Collector {
	return &Collector{
		history: make([]DataPoint, HistorySize),
	}
}

func (c *Collector) Record(ffmpegBitrate, srtlaBitrate, fps float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.history[c.pos] = DataPoint{
		Timestamp:     time.Now(),
		FFmpegBitrate: ffmpegBitrate,
		SRTLABitrate:  srtlaBitrate,
		FPS:           fps,
	}

	c.pos = (c.pos + 1) % HistorySize
	if c.pos == 0 {
		c.full = true
	}
}

func (c *Collector) History() []DataPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []DataPoint
	if c.full {
		result = make([]DataPoint, HistorySize)
		copy(result, c.history[c.pos:])
		copy(result[HistorySize-c.pos:], c.history[:c.pos])
	} else {
		result = make([]DataPoint, c.pos)
		copy(result, c.history[:c.pos])
	}
	return result
}

func (c *Collector) LatestBitrate() (ffmpeg, srtla float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.full && c.pos == 0 {
		return 0, 0
	}

	idx := c.pos - 1
	if idx < 0 {
		idx = HistorySize - 1
	}

	return c.history[idx].FFmpegBitrate, c.history[idx].SRTLABitrate
}

func (c *Collector) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history = make([]DataPoint, HistorySize)
	c.pos = 0
	c.full = false
}

type LogBuffer struct {
	mu    sync.RWMutex
	lines []LogEntry
	pos   int
	size  int
	full  bool
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Line      string    `json:"line"`
}

func NewLogBuffer(size int) *LogBuffer {
	return &LogBuffer{
		lines: make([]LogEntry, size),
		size:  size,
	}
}

func (b *LogBuffer) Add(source, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lines[b.pos] = LogEntry{
		Timestamp: time.Now(),
		Source:    source,
		Line:      line,
	}

	b.pos = (b.pos + 1) % b.size
	if b.pos == 0 {
		b.full = true
	}
}

func (b *LogBuffer) GetAll() []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []LogEntry
	if b.full {
		result = make([]LogEntry, b.size)
		copy(result, b.lines[b.pos:])
		copy(result[b.size-b.pos:], b.lines[:b.pos])
	} else {
		result = make([]LogEntry, b.pos)
		copy(result, b.lines[:b.pos])
	}
	return result
}

func (b *LogBuffer) GetRecent(n int) []LogEntry {
	all := b.GetAll()
	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}
