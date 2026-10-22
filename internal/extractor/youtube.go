package extractor

import (
	"strconv"
	"strings"
)

type YouTubeConfig struct {
	QualityPreference string
	DownloadSubtitles bool
	SubtitleLangs     []string
}
type YouTubeExtractor struct {
	*platformExtractor
	config YouTubeConfig
}

func NewYouTubeExtractor(cfg YouTubeConfig) *YouTubeExtractor {
	return &YouTubeExtractor{&platformExtractor{name: "youtube", domains: []string{"youtube.com", "youtu.be"}, subtitles: cfg.DownloadSubtitles, langs: cfg.SubtitleLangs}, cfg}
}
func (e *YouTubeExtractor) qualityRank(q string) int {
	q = strings.ToLower(q)
	for i, item := range []string{"4320", "2160", "1440", "1080", "720", "480", "360", "240", "144"} {
		if strings.Contains(q, item) {
			return []int{80, 70, 60, 50, 40, 30, 20, 10, 5}[i]
		}
	}
	switch q {
	case "8k":
		return 80
	case "4k":
		return 70
	case "2k":
		return 60
	case "audio_only":
		return 1
	}
	return 0
}
func parseHeight(q string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(q), "p"))
	return n
}
