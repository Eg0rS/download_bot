package extractor

type SoundCloudConfig struct{ Enabled bool }
type SoundCloudExtractor struct{ *platformExtractor }

func NewSoundCloudExtractor(cfg SoundCloudConfig) (*SoundCloudExtractor, error) {
	return &SoundCloudExtractor{&platformExtractor{name: "soundcloud", domains: []string{"soundcloud.com", "snd.sc"}}}, nil
}
