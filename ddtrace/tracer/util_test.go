// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"math"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/stretchr/testify/assert"
)

func TestParseUint64(t *testing.T) {
	t.Run("negative", func(t *testing.T) {
		id, err := parseUint64("-8809075535603237910")
		assert.NoError(t, err)
		assert.Equal(t, uint64(9637668538106313706), id)
	})

	t.Run("positive", func(t *testing.T) {
		id, err := parseUint64(fmt.Sprintf("%d", uint64(math.MaxUint64)))
		assert.NoError(t, err)
		assert.Equal(t, uint64(math.MaxUint64), id)
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := parseUint64("abcd")
		assert.Error(t, err)
	})
}

func TestIsValidPropagatableTraceTag(t *testing.T) {
	for i, tt := range [...]struct {
		key   string
		value string
		err   error
	}{
		{"hello", "world", nil},
		{"hello", "world=", nil},
		{"hello=", "world", fmt.Errorf("key contains an invalid character 61")},
		{"", "world", fmt.Errorf("key length must be greater than zero")},
		{"hello", "", fmt.Errorf("value length must be greater than zero")},
		{"こんにちは", "world", fmt.Errorf("key contains an invalid character 12371")},
		{"hello", "世界", fmt.Errorf("value contains an invalid character 19990")},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			assert.Equal(t, tt.err, isValidPropagatableTag(tt.key, tt.value))
		})
	}
}

func TestParsePropagatableTraceTags(t *testing.T) {
	for i, tt := range [...]struct {
		input  string
		output map[string]string
		err    error
	}{
		{"hello=world", map[string]string{"hello": "world"}, nil},
		{" hello = world ", map[string]string{" hello ": " world "}, nil},
		{"hello=world,service=account", map[string]string{"hello": "world", "service": "account"}, nil},
		{"hello=wor=ld====,service=account,tag1=val=ue1", map[string]string{"hello": "wor=ld====", "service": "account", "tag1": "val=ue1"}, nil},
		{"hello", nil, fmt.Errorf("invalid format")},
		{"hello=world,service=", nil, fmt.Errorf("invalid format")},
		{"hello=world,", nil, fmt.Errorf("invalid format")},
		{"=world", nil, fmt.Errorf("invalid format")},
		{"hello=,tag1=value1", nil, fmt.Errorf("invalid format")},
		{",hello=world", nil, fmt.Errorf("invalid format")},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			output, err := parsePropagatableTraceTags(tt.input)
			assert.Equal(t, tt.output, output)
			assert.Equal(t, tt.err, err)
		})
	}
}

func TestDereference(t *testing.T) {
	for i, tt := range []struct {
		value    interface{}
		expected interface{}
	}{
		{makePointer(1), 1},
		{makePointer(byte(1)), byte(1)},
		{makePointer(int16(1)), int16(1)},
		{makePointer(int32(1)), int32(1)},
		{makePointer(int64(1)), int64(1)},
		{makePointer(uint(1)), uint(1)},
		{makePointer(uint16(1)), uint16(1)},
		{makePointer(uint32(1)), uint32(1)},
		{makePointer(uint64(1)), uint64(1)},
		{makePointer("a"), "a"},
		{makePointer(float32(1.25)), float32(1.25)},
		{makePointer(float64(1.25)), float64(1.25)},
		{makePointer(true), true},
		{makePointer(false), false},
		{makePointer(samplernames.SingleSpan), samplernames.SingleSpan},
		{(*int)(nil), 0},
		{(*byte)(nil), byte(0)},
		{(*int16)(nil), int16(0)},
		{(*int32)(nil), int32(0)},
		{(*int64)(nil), int64(0)},
		{(*uint)(nil), uint(0)},
		{(*uint16)(nil), uint16(0)},
		{(*uint32)(nil), uint32(0)},
		{(*uint64)(nil), uint64(0)},
		{(*string)(nil), ""},
		{(*float32)(nil), float32(0)},
		{(*float64)(nil), float64(0)},
		{(*bool)(nil), false},
		{(*samplernames.SamplerName)(nil), samplernames.Unknown},
		{newSpan("test", "service", "resource", 1, 2, 0), "itself"}, // This test uses a value which type is not supported by dereference.
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			actual := dereference(tt.value)
			// This is a special case where we want to compare the value itself
			// because the dereference function returns the given value.
			if tt.expected == "itself" {
				if actual != tt.value {
					t.Fatalf("expected: %#v, got: %#v", tt.value, actual)
				}
				return
			}
			if actual != tt.expected {
				t.Fatalf("expected: %#v, got: %#v", tt.expected, actual)
			}
		})
	}
}

func makePointer[T any](value T) *T {
	return &value
}
