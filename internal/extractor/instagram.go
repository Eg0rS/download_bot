package extractor

type InstagramConfig struct{ Enabled bool }
type InstagramExtractor struct{ *platformExtractor }

func NewInstagramExtractor(cfg InstagramConfig) (*InstagramExtractor, error) {
	return &InstagramExtractor{&platformExtractor{name: "instagram", domains: []string{"instagram.com"}}}, nil
}
