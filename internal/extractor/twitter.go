package extractor

type TwitterConfig struct{ Enabled bool }
type TwitterExtractor struct{ *platformExtractor }

func NewTwitterExtractor(cfg TwitterConfig) (*TwitterExtractor, error) {
	return &TwitterExtractor{&platformExtractor{name: "twitter", domains: []string{"twitter.com", "x.com"}}}, nil
}
