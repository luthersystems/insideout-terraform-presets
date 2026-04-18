package composer

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoPresetFS is returned by preset-reading methods when the Client has no
// preset filesystem configured. Construct the Client with WithPresets.
var ErrNoPresetFS = errors.New("composer: no preset filesystem configured; pass composer.WithPresets")

var allowedExt = map[string]bool{
	".tf": true, ".tfvars": true, ".tf.json": true, ".terraform-version": true, ".tmpl": true, ".zip": true,
}

// ListClouds returns the available cloud providers (aws, gcp, etc).
func (c *Client) ListClouds() ([]string, error) {
	if c.presets == nil {
		return nil, ErrNoPresetFS
	}
	ents, err := fs.ReadDir(c.presets, ".")
	if err != nil {
		return nil, err
	}
	var clouds []string
	for _, e := range ents {
		if e.IsDir() {
			clouds = append(clouds, e.Name())
		}
	}
	sort.Strings(clouds)
	return clouds, nil
}

// ListAvailableComponentKeys returns all ComponentKey values that have presets.
func (c *Client) ListAvailableComponentKeys() ([]string, error) {
	if c.presets == nil {
		return nil, ErrNoPresetFS
	}
	clouds, err := c.ListClouds()
	if err != nil {
		return nil, err
	}

	keySet := make(map[string]bool)
	// Always include composer if we have any presets
	if len(clouds) > 0 {
		keySet[string(KeyComposer)] = true
	}

	// We need to check all possible ComponentKeys and see if they have a preset
	// This is slightly inefficient but ensures consistency with GetPresetPath
	allKeys := []ComponentKey{
		KeyVPC, KeyBastion, KeyEC2, KeyResource, KeyALB, KeyCloudfront, KeyWAF,
		KeyPostgres, KeyElastiCache, KeyS3, KeyDynamoDB, KeySQS, KeyMSK,
		KeyCloudWatchLogs, KeyCloudWatchMonitoring, KeySplunk, KeyDatadog,
		KeyGrafana, KeyCognito, KeyBackups, KeyGitHubActions, KeyCodePipeline,
		KeyLambda, KeyAPIGateway, KeyKMS, KeySecrets, KeyOpenSearch, KeyBedrock,

		// AWS prefixed keys
		KeyAWSVPC, KeyAWSBastion, KeyAWSEC2, KeyAWSEKS, KeyAWSECS, KeyAWSLambda,
		KeyAWSALB, KeyAWSCloudfront, KeyAWSWAF, KeyAWSAPIGateway, KeyAWSRDS,
		KeyAWSElastiCache, KeyAWSDynamoDB, KeyAWSS3, KeyAWSKMS, KeyAWSSecretsManager,
		KeyAWSOpenSearch, KeyAWSBedrock, KeyAWSSQS, KeyAWSMSK, KeyAWSCloudWatchLogs, KeyAWSCloudWatchMonitoring,
		KeyAWSGrafana, KeyAWSCognito, KeyAWSBackups, KeyAWSGitHubActions, KeyAWSCodePipeline,

		// GCP keys
		KeyGCPVPC, KeyGCPBastion, KeyGCPCompute, KeyGCPGKE, KeyGCPCloudRun,
		KeyGCPCloudFunctions, KeyGCPLoadbalancer, KeyGCPCloudCDN, KeyGCPCloudArmor,
		KeyGCPAPIGateway, KeyGCPCloudSQL, KeyGCPMemorystore, KeyGCPFirestore,
		KeyGCPGCS, KeyGCPCloudKMS, KeyGCPSecretManager, KeyGCPVertexAI,
		KeyGCPPubSub, KeyGCPCloudLogging, KeyGCPCloudMonitoring,
		KeyGCPIdentityPlatform, KeyGCPCloudBuild, KeyGCPBackups,
	}

	for _, cloud := range clouds {
		for _, k := range allKeys {
			path := GetPresetPath(cloud, k, nil)
			if ents, err := fs.ReadDir(c.presets, path); err == nil && len(ents) > 0 {
				keySet[string(k)] = true
			}
		}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// ListPresetKeysForCloud lists module keys for a specific cloud provider.
// Returns keys like "aws/vpc", "aws/ec2", etc.
func (c *Client) ListPresetKeysForCloud(cloud string) ([]string, error) {
	if c.presets == nil {
		return nil, ErrNoPresetFS
	}
	ents, err := fs.ReadDir(c.presets, cloud)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, e := range ents {
		if e.IsDir() {
			keys = append(keys, cloud+"/"+e.Name())
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// GetPresetFiles returns a map of "/<relpath>" -> file bytes for a given module key.
func (c *Client) GetPresetFiles(key string) (map[string][]byte, error) {
	if c.presets == nil {
		return nil, ErrNoPresetFS
	}
	out := map[string][]byte{}
	err := fs.WalkDir(c.presets, key, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !allowedExt[ext] {
			return nil
		}
		b, err := fs.ReadFile(c.presets, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, key)
		rel = strings.TrimPrefix(rel, "/")
		out["/"+rel] = b
		return nil
	})
	return out, err
}
