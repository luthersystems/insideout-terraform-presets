package iamaudit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/sync/errgroup"
)

// Source feeds the CloudTrail events of the collector's window into it.
type Source interface {
	Feed(ctx context.Context, c *Collector) error
}

// errNoWindow is returned by the AWS sources, which must bound what they
// read: an open window would scan the whole history or bucket.
var errNoWindow = errors.New("the collector needs a Start and an End")

// LookupEventsAPI is the CloudTrail call the event-history source uses. The
// *cloudtrail.Client of one region satisfies it.
type LookupEventsAPI interface {
	LookupEvents(ctx context.Context, in *cloudtrail.LookupEventsInput, optFns ...func(*cloudtrail.Options)) (*cloudtrail.LookupEventsOutput, error)
}

// LookupSource reads CloudTrail event history (LookupEvents), which needs no
// trail. It holds 90 days of management events per region and no data
// events, and is regional: IAM, STS (global endpoint), CloudFront and Route 53
// log in us-east-1, so include it in the region list.
type LookupSource struct {
	// Clients maps a region to its client.
	Clients map[string]LookupEventsAPI
	// Interval spaces the calls to one region. LookupEvents allows 2
	// requests per second per account and region; the SDK retries a
	// throttle, but pacing avoids the retry storm on a long window.
	Interval time.Duration
}

// Feed reads every event in the collector's window of each region into c.
// LookupEvents cannot filter by role, so it reads all events and the
// collector filters.
func (s *LookupSource) Feed(ctx context.Context, c *Collector) error {
	if c.Start.IsZero() || c.End.IsZero() {
		return errNoWindow
	}
	start, end := c.Start, c.End
	// Sorted, so a run reads (and fails on) regions in a stable order.
	for _, region := range slices.Sorted(maps.Keys(s.Clients)) {
		client := s.Clients[region]
		in := &cloudtrail.LookupEventsInput{
			StartTime:  aws.Time(start),
			EndTime:    aws.Time(end),
			MaxResults: aws.Int32(50),
		}
		p := cloudtrail.NewLookupEventsPaginator(client, in)
		for p.HasMorePages() {
			out, err := p.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("lookup events in %s: %w", region, err)
			}
			for _, e := range out.Events {
				if e.CloudTrailEvent == nil {
					continue
				}
				ev, err := ParseEvent([]byte(*e.CloudTrailEvent))
				if err != nil {
					return fmt.Errorf("lookup events in %s: %w", region, err)
				}
				c.Add(ev)
			}
			if s.Interval > 0 && p.HasMorePages() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(s.Interval):
				}
			}
		}
	}
	return nil
}

// S3API is the S3 calls the trail-bucket source uses.
type S3API interface {
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3Source reads the log files a trail delivered to its S3 bucket. It sees
// everything the trail records, data events included when the trail logs
// them, with no 90-day limit and no throttle to speak of.
type S3Source struct {
	Client S3API
	Bucket string
	// Prefix is the key path down to the account, without a trailing
	// slash: "AWSLogs/<account>" for an account trail (behind the trail's
	// own S3 key prefix if it has one), "AWSLogs/<org-id>/<account>" for an
	// organization trail.
	Prefix  string
	Regions []string
	// Workers is how many log files are read at once (default 16).
	Workers int
}

// Feed reads the log files for the collector's window of each region into c. A trail
// files a log under the UTC day of its delivery, which can trail the events
// in it by up to about 15 minutes, so the listing takes in the day after end;
// the collector drops the events outside the window.
func (s *S3Source) Feed(ctx context.Context, c *Collector) error {
	if c.Start.IsZero() || c.End.IsZero() {
		return errNoWindow
	}
	keys, err := s.listKeys(ctx, c.Start, c.End)
	if err != nil {
		return err
	}
	workers := s.Workers
	if workers <= 0 {
		workers = 16
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for _, key := range keys {
		g.Go(func() error {
			out, err := s.Client.GetObject(gctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
			if err != nil {
				return fmt.Errorf("get s3://%s/%s: %w", s.Bucket, key, err)
			}
			defer func() { _ = out.Body.Close() }() // read only: a close error loses no data
			zr, err := gzip.NewReader(out.Body)
			if err != nil {
				return fmt.Errorf("s3://%s/%s: gunzip: %w", s.Bucket, key, err)
			}
			defer func() { _ = zr.Close() }() // read only: a close error loses no data
			// Streamed one record at a time, so 16 workers hold 16
			// records, not 16 files. The limit only stops a hostile
			// object from decompressing for ever.
			r := io.LimitReader(zr, maxLogFile)
			if err := ParseTrailStream(r, func(ev Event) error { c.Add(ev); return nil }); err != nil {
				return fmt.Errorf("s3://%s/%s: %w", s.Bucket, key, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// listKeys lists the log files of each region and day of the window.
func (s *S3Source) listKeys(ctx context.Context, start, end time.Time) ([]string, error) {
	var keys []string
	last := end.UTC().AddDate(0, 0, 1)
	// A region named twice would be read twice and its calls counted twice.
	for _, region := range slices.Compact(slices.Sorted(slices.Values(s.Regions))) {
		for day := start.UTC().Truncate(24 * time.Hour); !day.After(last); day = day.AddDate(0, 0, 1) {
			prefix := fmt.Sprintf("%s/CloudTrail/%s/%s/", strings.TrimSuffix(s.Prefix, "/"), region, day.Format("2006/01/02"))
			p := s3.NewListObjectsV2Paginator(s.Client, &s3.ListObjectsV2Input{Bucket: aws.String(s.Bucket), Prefix: aws.String(prefix)})
			for p.HasMorePages() {
				out, err := p.NextPage(ctx)
				if err != nil {
					return nil, fmt.Errorf("list s3://%s/%s: %w", s.Bucket, prefix, err)
				}
				for _, o := range out.Contents {
					if o.Key != nil && strings.HasSuffix(*o.Key, ".json.gz") {
						keys = append(keys, *o.Key)
					}
				}
			}
		}
	}
	return keys, nil
}

// maxLogFile caps one decompressed log file. CloudTrail delivers a file
// every 5 minutes per region, a few MB at most; the cap stops a corrupt or
// hostile object from exhausting memory (a local file is read whole) or
// decompressing for ever (an S3 object is streamed).
const maxLogFile = 256 << 20

// readLogFile reads a log file, decompressing it when name ends in ".gz".
func readLogFile(r io.Reader, name string) ([]byte, error) {
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("gunzip: %w", err)
		}
		defer func() { _ = zr.Close() }() // read only: a close error loses no data
		r = zr
	}
	data, err := io.ReadAll(io.LimitReader(r, maxLogFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLogFile {
		return nil, fmt.Errorf("larger than %d bytes", maxLogFile)
	}
	return data, nil
}

// FileSource reads CloudTrail records from local files: trail log files
// (.json or .json.gz, as `aws s3 sync` of a trail bucket leaves them) and
// the JSON of `aws cloudtrail lookup-events`. A directory is walked.
type FileSource struct {
	Paths []string
}

// skipDirs are the folders `aws s3 sync` of a trail bucket brings along that
// hold no API calls: digest files (log file validation) and Insights events.
var skipDirs = map[string]bool{"CloudTrail-Digest": true, "CloudTrail-Insight": true}

// Feed reads every file into c. A file reached twice (a directory and a file
// in it both given) is read once, so no call is counted twice.
func (s *FileSource) Feed(_ context.Context, c *Collector) error {
	seen := map[string]bool{}
	for _, root := range s.Paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".json.gz") {
				return nil
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if seen[abs] {
				return nil
			}
			seen[abs] = true
			return feedFile(path, c)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func feedFile(path string, c *Collector) error {
	f, err := os.Open(path) //nolint:gosec // G304: the operator names the files to audit
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read only: a close error loses no data
	data, err := readLogFile(f, path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := parseAnyFile(data, c); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// parseAnyFile accepts a trail log file ({"Records": [...]}) or the output
// of `aws cloudtrail lookup-events` ({"Events": [{"CloudTrailEvent": "..."}]}).
func parseAnyFile(data []byte, c *Collector) error {
	var probe struct {
		Records json.RawMessage `json:"Records"`
		Events  []struct {
			CloudTrailEvent *string `json:"CloudTrailEvent"`
		} `json:"Events"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	switch {
	case len(bytes.TrimSpace(probe.Records)) > 0:
		return ParseTrailStream(bytes.NewReader(data), func(ev Event) error { c.Add(ev); return nil })
	case probe.Events != nil:
		for i, e := range probe.Events {
			if e.CloudTrailEvent == nil {
				continue
			}
			ev, err := ParseEvent([]byte(*e.CloudTrailEvent))
			if err != nil {
				return fmt.Errorf("event %d: %w", i, err)
			}
			c.Add(ev)
		}
		return nil
	default:
		return errors.New(`neither a trail log file ("Records") nor lookup-events output ("Events")`)
	}
}
