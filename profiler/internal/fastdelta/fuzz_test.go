// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package fastdelta_test

import (
	"io"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/profiler/internal/fastdelta"
)

// FuzzDelta looks for inputs to delta which cause crashes. This is to account
// for the possibility that the profile format changes in some way, or violates
// any hard-coded assumptions.
func FuzzDelta(f *testing.F) {
	f.Fuzz(func(_ *testing.T, b []byte) {
		dc := fastdelta.NewDeltaComputer()
		dc.Delta(b, io.Discard)
	})
}
