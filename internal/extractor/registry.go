package extractor

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

type Registry struct {
	mu         sync.RWMutex
	extractors map[string]Extractor
	ordered    []Extractor
}

func NewRegistry() *Registry {
	return &Registry{
		extractors: make(map[string]Extractor),
		ordered:    make([]Extractor, 0),
	}
}

func (r *Registry) Register(e Extractor) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := e.Name()
	if _, exists := r.extractors[name]; exists {
		for i, previous := range r.ordered {
			if previous.Name() == name {
				r.ordered[i] = e
				break
			}
		}
		r.extractors[name] = e
		return
	}
	r.extractors[name] = e
	r.ordered = append(r.ordered, e)
	logrus.Infof("Registered extractor: %s", name)
}

func (r *Registry) Get(name string) (Extractor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.extractors[name]
	return e, ok
}

func (r *Registry) FindForURL(url string) (Extractor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.ordered {
		if e.CanHandle(url) {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no extractor found for URL: %s", url)
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.extractors))
	for name := range r.extractors {
		names = append(names, name)
	}
	return names
}

func (r *Registry) Extract(ctx context.Context, url string) (*MediaInfo, error) {
	e, err := r.FindForURL(url)
	if err != nil {
		return nil, err
	}
	logrus.Infof("Using extractor %s for %s", e.Name(), url)
	return e.Extract(ctx, url)
}

func (r *Registry) Download(ctx context.Context, url string, qualityPref string, audioOnly bool, progress ProgressFunc) (*DownloadResult, error) {
	if err := ValidateURL(url); err != nil {
		return nil, err
	}
	e, err := r.FindForURL(url)
	if err != nil {
		return nil, err
	}

	info, err := e.Extract(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("extract failed: %w", err)
	}

	format, err := e.GetBestFormat(info, qualityPref, audioOnly)
	if err != nil {
		return nil, fmt.Errorf("format selection failed: %w", err)
	}

	logrus.Infof("Downloading with format: %s (%s, %s)", format.FormatID, format.Quality, format.Ext)
	result, err := e.Download(ctx, url, format, progress)
	if err != nil {
		return nil, err
	}
	if result == nil || result.FilePath == "" {
		return nil, fmt.Errorf("extractor returned no file")
	}
	result.Title, result.Duration, result.Thumbnail = info.Title, info.Duration, info.Thumbnail
	return result, nil
}

func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, e := range r.ordered {
		if closer, ok := e.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				logrus.Errorf("Error closing extractor %s: %v", e.Name(), err)
			}
		}
	}
	return nil
}
