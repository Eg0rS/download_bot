package extractor

type TikTokConfig struct{ Watermark bool }
type TikTokExtractor struct{ *platformExtractor }

func NewTikTokExtractor(cfg TikTokConfig) (*TikTokExtractor, error) {
	return &TikTokExtractor{&platformExtractor{name: "tiktok", domains: []string{"tiktok.com"}}}, nil
}
