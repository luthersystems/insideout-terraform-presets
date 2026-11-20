package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/iamaudit"
)

const fixtures = "../../pkg/iamaudit/testdata/"

// noAWS fails any test that would reach an AWS source.
func noAWS(context.Context, *options) (iamaudit.Source, error) {
	return nil, errors.New("test must not build an AWS source")
}

func baseArgs(policy string) []string {
	return []string{
		"-source", "file",
		"-file", fixtures + "trail-records.json",
		"-role-regex", `:role/[^/]+-luther-terraform$`,
		"-start", "2026-11-10T00:00:00Z",
		"-end", "2026-11-11T00:00:00Z",
		"-policy", policy,
	}
}

func TestRunGapExits3(t *testing.T) {
	jsonOut := filepath.Join(t.TempDir(), "report.json")
	args := append(baseArgs(fixtures+"insideout-write.json"), "-boundary", fixtures+"insideout-deploy-boundary.json", "-json", jsonOut)
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, &out, &errOut, noAWS)
	assert.Equal(t, exitGap, code, errOut.String())
	assert.Contains(t, out.String(), "Phase 2 gate: FAIL")

	data, err := os.ReadFile(jsonOut)
	require.NoError(t, err)
	var rep iamaudit.Report
	require.NoError(t, json.Unmarshal(data, &rep))
	assert.Len(t, rep.UsedNotAllowed, 2)
	assert.Equal(t, "file", rep.Source)

	// The same audit without the gate exits 0.
	out.Reset()
	assert.Equal(t, exitOK, run(context.Background(), append(args, "-fail-on-gap=false"), &out, &errOut, noAWS))
}

func TestRunCleanJSON(t *testing.T) {
	admin := filepath.Join(t.TempDir(), "admin.json")
	require.NoError(t, os.WriteFile(admin, []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`), 0o600))
	var out, errOut bytes.Buffer
	code := run(context.Background(), append(baseArgs(admin), "-format", "json"), &out, &errOut, noAWS)
	require.Equal(t, exitOK, code, errOut.String())
	var rep iamaudit.Report
	require.NoError(t, json.Unmarshal(out.Bytes(), &rep))
	assert.True(t, rep.OK())
	assert.Equal(t, 19, rep.EventsMatched)
}

func TestRunAWSSourceUsesFactory(t *testing.T) {
	admin := filepath.Join(t.TempDir(), "admin.json")
	require.NoError(t, os.WriteFile(admin, []byte(`{"Statement":{"Effect":"Allow","Action":"*","Resource":"*"}}`), 0o600))
	var got *options
	factory := func(_ context.Context, o *options) (iamaudit.Source, error) {
		got = o
		return &iamaudit.FileSource{Paths: []string{fixtures + "trail-records.json"}}, nil
	}
	args := []string{
		"-source", "s3", "-bucket", "trail", "-s3-prefix", "AWSLogs/111122223333",
		"-region", "us-west-2,us-east-1", "-region", "us-west-2",
		// A comma inside a regex stays in the regex.
		"-role-regex", `^arn:aws:iam::\d{12}:role/[a-z0-9]{1,8}-luther-terraform$`, "-since", "720h", "-end", "2026-11-11T00:00:00Z",
		"-policy", admin,
	}
	var out, errOut bytes.Buffer
	require.Equal(t, exitOK, run(context.Background(), args, &out, &errOut, factory), errOut.String())
	require.NotNil(t, got)
	assert.Equal(t, regionList{"us-west-2", "us-east-1"}, got.regions)
	assert.Equal(t, multi{`^arn:aws:iam::\d{12}:role/[a-z0-9]{1,8}-luther-terraform$`}, got.roleRegex)
	assert.Contains(t, out.String(), "Phase 2 gate: PASS")
	assert.Contains(t, out.String(), "Data events are in this report only if the trail logs them.")
}

func TestRunUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"no role":          {"-source", "file", "-file", "x", "-policy", "p"},
		"no policy":        {"-source", "file", "-file", "x", "-role-arn", "a"},
		"bad source":       {"-source", "athena", "-role-arn", "a", "-policy", "p"},
		"s3 needs bucket":  {"-source", "s3", "-role-arn", "a", "-policy", "p", "-since", "1h"},
		"lookup needs reg": {"-source", "lookup", "-role-arn", "a", "-policy", "p", "-since", "1h"},
		"aws needs window": {"-source", "lookup", "-region", "us-east-1", "-role-arn", "a", "-policy", "p"},
		"start and since":  {"-source", "file", "-file", "x", "-role-arn", "a", "-policy", "p", "-start", "2026-11-10T00:00:00Z", "-since", "1h"},
		"bad format":       {"-source", "file", "-file", "x", "-role-arn", "a", "-policy", "p", "-format", "yaml"},
		"extra arg":        {"-source", "file", "-file", "x", "-role-arn", "a", "-policy", "p", "extra"},
		"unknown flag":     {"-nope"},
		"negative since":   {"-source", "file", "-file", "x", "-role-arn", "a", "-policy", "p", "-since", "-1h"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			assert.Equal(t, exitUsage, run(context.Background(), args, &out, &errOut, noAWS))
		})
	}
}

func TestRunErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	// A missing policy file.
	assert.Equal(t, exitError, run(context.Background(), baseArgs("nope.json"), &out, &errOut, noAWS))
	// A bad role pattern.
	args := append(baseArgs(fixtures+"insideout-write.json"), "-role-regex", "(")
	assert.Equal(t, exitError, run(context.Background(), args, &out, &errOut, noAWS))
	// An empty window.
	args = []string{"-source", "file", "-file", "x", "-role-arn", "a", "-policy", fixtures + "insideout-write.json",
		"-start", "2026-11-11T00:00:00Z", "-end", "2026-11-10T00:00:00Z"}
	assert.Equal(t, exitError, run(context.Background(), args, &out, &errOut, noAWS))
	assert.Contains(t, errOut.String(), "the window is empty")
}

func TestWindow(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	s, e, err := window(&options{since: 24 * time.Hour}, now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-24*time.Hour), s)
	assert.Equal(t, now, e)

	s, _, err = window(&options{}, now)
	require.NoError(t, err)
	assert.True(t, s.IsZero(), "the file source may leave the start open")

	_, _, err = window(&options{end: "yesterday"}, now)
	assert.Error(t, err)
	_, _, err = window(&options{start: "today"}, now)
	assert.Error(t, err)
}

func TestNotes(t *testing.T) {
	n := notes(&options{source: "lookup", regions: regionList{"us-west-2"}})
	assert.Len(t, n, 3)
	assert.Contains(t, n[1], "management events only")
	assert.Contains(t, n[2], "us-east-1 was not read")
	assert.Len(t, notes(&options{source: "file"}), 1)
}
