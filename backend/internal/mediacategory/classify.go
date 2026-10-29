// Package mediacategory classifies caller-supplied media facts using ordered
// YAML rules. It performs no metadata requests, storage reads or file operations.
package mediacategory

import (
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
)

//go:embed default.yaml
var defaultYAML string

var (
	ErrConfig = errors.New("分类规则无效")
	ErrFacts  = errors.New("分类信息无效")
)

// Facts keeps origin and production countries separate. Missing facts never
// inherit another field; the caller may pass the metadata it already has.
type Facts struct {
	Kind                string   `json:"kind"`
	Genres              []int    `json:"genres"`
	OriginCountries     []string `json:"originCountries"`
	ProductionCountries []string `json:"productionCountries"`
	OriginalLanguage    string   `json:"originalLanguage"`
}

type Result struct {
	Path     string `json:"path"`
	RuleName string `json:"ruleName,omitempty"`
	Fallback bool   `json:"fallback"`
}

type conditions struct {
	genres              []int
	originCountries     []string
	productionCountries []string
	languages           []string
}

type rule struct {
	name, path string
	match      conditions
}

type section struct {
	fallback string
	rules    []rule
}

// Classifier owns its validated rules. Compile once per configuration snapshot;
// Classify can then be called concurrently without mutating or retaining facts.
type Classifier struct{ movie, tv section }

// DefaultYAML is the single embedded default for loading, editing and resets.
func DefaultYAML() string { return defaultYAML }

func (c *Classifier) Classify(facts Facts) (Result, error) {
	if c == nil {
		return Result{}, fmt.Errorf("%w：请先加载规则", ErrConfig)
	}
	var selected *section
	switch facts.Kind {
	case "movie":
		selected = &c.movie
	case "tv":
		selected = &c.tv
	default:
		return Result{}, fmt.Errorf("%w：不支持的作品类型 %q", ErrFacts, facts.Kind)
	}
	if selected.fallback == "" {
		return Result{}, fmt.Errorf("%w：请先加载规则", ErrConfig)
	}
	for _, rule := range selected.rules {
		if rule.match.matches(facts) {
			return Result{Path: rule.path, RuleName: rule.name}, nil
		}
	}
	return Result{Path: selected.fallback, Fallback: true}, nil
}

func (m conditions) matches(f Facts) bool {
	if len(m.genres) > 0 {
		found := false
		for _, genre := range f.Genres {
			if slices.Contains(m.genres, genre) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return matchesCodes(m.originCountries, f.OriginCountries...) &&
		matchesCodes(m.productionCountries, f.ProductionCountries...) &&
		matchesCodes(m.languages, f.OriginalLanguage)
}

func matchesCodes(expected []string, actual ...string) bool {
	if len(expected) == 0 {
		return true
	}
	for _, value := range actual {
		value = strings.TrimSpace(value)
		if !validCode(value) {
			continue
		}
		for _, candidate := range expected {
			if strings.EqualFold(value, candidate) {
				return true
			}
		}
	}
	return false
}

func validCode(value string) bool {
	return len(value) == 2 && asciiLetter(value[0]) && asciiLetter(value[1])
}

func asciiLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
