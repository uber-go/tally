// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package tally

import (
	"bytes"
)

var (
	// DefaultReplacementCharacter is the default character used for
	// replacements.
	DefaultReplacementCharacter = '_'

	// AlphanumericRange is the range of alphanumeric characters.
	AlphanumericRange = []SanitiseRange{
		{rune('a'), rune('z')},
		{rune('A'), rune('Z')},
		{rune('0'), rune('9')}}

	// UnderscoreDashCharacters is a slice of underscore, and
	// dash characters.
	UnderscoreDashCharacters = []rune{
		'-',
		'_'}

	// UnderscoreDashDotCharacters is a slice of underscore,
	// dash, and dot characters.
	UnderscoreDashDotCharacters = []rune{
		',',
		'-',
		'_'}
)

// SanitiseFn returns a sanitised version of the input string.
type SanitiseFn func(string) string

// SanitiseRange is a range of characters (inclusive on both ends).
type SanitiseRange [2]rune

// ValidCharacters is a collection of valid characters.
type ValidCharacters struct {
	Ranges     []SanitiseRange
	Characters []rune
}

// SanitiseOptions are the set of configurable options for sanitisation.
type SanitiseOptions struct {
	ValidNameCharacters  ValidCharacters
	ValidKeyCharacters   ValidCharacters
	ValidValueCharacters ValidCharacters
	ReplacementCharacter rune
}

// Sanitiser sanitises the provided input based on the function called.
type Sanitiser interface {
	// Name sanitises the provided `name` string.
	Name(n string) string

	// Key sanitises the provided `key` string.
	Key(k string) string

	// Value sanitises the provided `value` string.
	Value(v string) string
}

// NewSanitiser returns a new sanitiser based on provided options
func NewSanitiser(opts SanitiseOptions) Sanitiser {
	return &santiser{
		nameFn:  opts.ValidNameCharacters.sanitiseFn(opts.ReplacementCharacter),
		keyFn:   opts.ValidKeyCharacters.sanitiseFn(opts.ReplacementCharacter),
		valueFn: opts.ValidValueCharacters.sanitiseFn(opts.ReplacementCharacter),
	}
}

// NoOpSanitizeFn returns the input un-touched.
func NoOpSanitizeFn(v string) string { return v }

// NewNoOpSanitiser returns a sanitizer which returns all inputs un-touched.
func NewNoOpSanitiser() Sanitiser {
	return &santiser{
		nameFn:  NoOpSanitizeFn,
		keyFn:   NoOpSanitizeFn,
		valueFn: NoOpSanitizeFn,
	}
}

type santiser struct {
	nameFn  SanitiseFn
	keyFn   SanitiseFn
	valueFn SanitiseFn
}

func (s *santiser) Name(n string) string {
	return s.nameFn(n)
}

func (s *santiser) Key(k string) string {
	return s.keyFn(k)
}

func (s *santiser) Value(v string) string {
	return s.valueFn(v)
}

func (c *ValidCharacters) sanitiseFn(repChar rune) SanitiseFn {
	return func(value string) string {
		var buf *bytes.Buffer
		for idx, ch := range value {
			// first check if the provided character is valid
			validCurr := false
			for i := 0; !validCurr && i < len(c.Ranges); i++ {
				for j := c.Ranges[i][0]; j <= c.Ranges[i][1]; j++ {
					if rune(j) == ch {
						validCurr = true
						break
					}
				}
			}
			for i := 0; !validCurr && i < len(c.Characters); i++ {
				if c.Characters[i] == ch {
					validCurr = true
					break
				}
			}

			// if it's valid, we can optimise allocations by avoiding
			// copying unless necessary
			if validCurr {
				if buf == nil {
					continue // haven't deviated from string
				}
				buf.WriteRune(ch) // we've deviated from string, write to buffer
				continue
			}

			// ie the character is invalid, and the buffer has not been initialised
			// so we initialise buffer and backfill
			if buf == nil {
				buf = bytes.NewBuffer(make([]byte, 0, len(value)))
				if idx > 0 {
					buf.WriteString(value[:idx])
				}
			}

			// write the replacement character
			buf.WriteRune(repChar)
		}

		// return input un-touched if the buffer has been not initialised
		if buf == nil {
			return value
		}

		// otherwise, return the newly constructed buffer
		return buf.String()
	}
}
