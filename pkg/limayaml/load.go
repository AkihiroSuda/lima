package limayaml

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"github.com/lima-vm/lima/pkg/store/dirnames"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/sirupsen/logrus"
	yamlv3 "gopkg.in/yaml.v3"
)

func unmarshalDisk(dst *Disk, b []byte) error {
	var s string
	if err := yaml.Unmarshal(b, &s); err == nil {
		*dst = Disk{Name: s}
		return nil
	}
	return yaml.Unmarshal(b, dst)
}

func (d *Disk) UnmarshalYAML(value *yamlv3.Node) error {
	var v interface{}
	if err := value.Decode(&v); err != nil {
		return err
	}
	if s, ok := v.(string); ok {
		*d = Disk{Name: s}
	}
	return nil
}

func unmarshalStringArray(dst *StringArray, b []byte) error {
	var multi []string
	if err := yaml.Unmarshal(b, &multi); err == nil {
		*dst = multi
	} else {
		var single string
		if err := yaml.Unmarshal(b, &single); err != nil {
			return err
		}
		*dst = []string{single}
	}
	return nil
}

func (a *StringArray) UnmarshalYAML(value *yamlv3.Node) error {
	var multi []string
	if err := value.Decode(&multi); err == nil {
		*a = multi
	} else {
		var single string
		if err := value.Decode(&single); err != nil {
			return err
		}
		*a = []string{single}
	}
	return nil
}

func unmarshalYAML(data []byte, v interface{}, comment string) error {
	decoderOptions := []yaml.DecodeOption{yaml.CustomUnmarshaler(unmarshalDisk), yaml.CustomUnmarshaler(unmarshalStringArray)}
	if err := yaml.UnmarshalWithOptions(data, v, append(decoderOptions, yaml.DisallowDuplicateKey())...); err != nil {
		return fmt.Errorf("failed to unmarshal YAML (%s): %w", comment, err)
	}
	// the go-yaml library doesn't catch all markup errors, unfortunately
	// make sure to get a "second opinion", using the same library as "yq"
	if err := yamlv3.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to unmarshal YAML (%s): %w", comment, err)
	}
	if err := yaml.UnmarshalWithOptions(data, v, append(decoderOptions, yaml.Strict())...); err != nil {
		logrus.WithField("comment", comment).WithError(err).Warn("Non-strict YAML is deprecated and will be unsupported in a future version of Lima")
		// Non-strict YAML is known to be used by Rancher Desktop:
		// https://github.com/rancher-sandbox/rancher-desktop/blob/c7ea7508a0191634adf16f4675f64c73198e8d37/src/backend/lima.ts#L114-L117
	}
	return nil
}

type options struct {
	enableHostProvision bool // default: false
}

type Opt func(*options)

func WithEnableHostProvision() Opt {
	return func(o *options) {
		o.enableHostProvision = true
	}
}

// Load loads the yaml and fulfills unspecified fields with the default values.
//
// Load does not validate. Use Validate for validation.
func Load(b []byte, filePath string, opts ...Opt) (*LimaYAML, error) {
	var y, d, o LimaYAML

	if err := unmarshalYAML(b, &y, fmt.Sprintf("main file %q", filePath)); err != nil {
		return nil, err
	}
	configDir, err := dirnames.LimaConfigDir()
	if err != nil {
		return nil, err
	}

	defaultPath := filepath.Join(configDir, filenames.Default)
	bytes, err := os.ReadFile(defaultPath)
	if err == nil {
		logrus.Debugf("Mixing %q into %q", defaultPath, filePath)
		if err := unmarshalYAML(bytes, &d, fmt.Sprintf("default file %q", defaultPath)); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	overridePath := filepath.Join(configDir, filenames.Override)
	bytes, err = os.ReadFile(overridePath)
	if err == nil {
		logrus.Debugf("Mixing %q into %q", overridePath, filePath)
		if err := unmarshalYAML(bytes, &o, fmt.Sprintf("override file %q", overridePath)); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	FillDefault(&y, &d, &o, filePath, opts...)
	return &y, nil
}
