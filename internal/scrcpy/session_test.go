package scrcpy

import (
	"errors"
	"testing"
)

func TestNeedsPush(t *testing.T) {
	const localSize = 71200

	tests := []struct {
		name       string
		localSize  int64
		statOutput []byte
		statErr    error
		want       bool
	}{
		{
			name:       "remote file missing",
			localSize:  localSize,
			statOutput: []byte("stat: '/data/local/tmp/sim-go-scrcpy-2.7.jar': No such file or directory\n"),
			statErr:    errors.New("exit status 1"),
			want:       true,
		},
		{
			name:       "remote file empty",
			localSize:  localSize,
			statOutput: []byte("0\n"),
			statErr:    nil,
			want:       true,
		},
		{
			name:       "remote file truncated or different size",
			localSize:  localSize,
			statOutput: []byte("50000\n"),
			statErr:    nil,
			want:       true,
		},
		{
			name:       "exact match",
			localSize:  localSize,
			statOutput: []byte("71200\n"),
			statErr:    nil,
			want:       false,
		},
		{
			name:       "exact match with carriage return and spaces",
			localSize:  localSize,
			statOutput: []byte("  71200\r\n"),
			statErr:    nil,
			want:       false,
		},
		{
			name:       "garbage output",
			localSize:  localSize,
			statOutput: []byte("unknown output\n"),
			statErr:    nil,
			want:       true,
		},
		{
			name:       "negative size output",
			localSize:  localSize,
			statOutput: []byte("-1\n"),
			statErr:    nil,
			want:       true,
		},
		{
			name:       "invalid local size",
			localSize:  0,
			statOutput: []byte("0\n"),
			statErr:    nil,
			want:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := needsPush(tc.localSize, tc.statOutput, tc.statErr)
			if got != tc.want {
				t.Fatalf("needsPush(%d, %q, %v) = %v; want %v", tc.localSize, tc.statOutput, tc.statErr, got, tc.want)
			}
		})
	}
}
