package limatmpl_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lima-vm/lima/pkg/limatmpl"
	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/sirupsen/logrus"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

type embedTestCase struct {
	description string
	template    string
	base        string
	expected    string
}

// Notes:
// * When the template starts with "#" then the comparison will be textual instead of structural.
//   This is required to verify comment handling.
// * If the description starts with "TODO" then the test is expected to fail (until it is fixed).
// * If the description starts with "ERROR" then the test is expected to fail with an error containing the expected string.
// * base is split on "---\n" and stored as base0.yaml, base1.yaml, ... in the same dir as the template.
// * If any base template starts with "#!" then the extension will be .sh instead of .yaml.
// * The template is automatically prefixed with "base: base0.yaml" unless base0 is a script.
// * All line comments will be separated by 2 spaces from the value on output.
// * Merge order of additionalDisks, mounts, and networks depends on the logic in the
//   combineListEntries() functions and will not follow the order of the base template(s).

var embedTestCases = []embedTestCase{
	{
		"Empty template",
		"",
		"vmType: qemu",
		"vmType: qemu",
	},
	{
		"Base doesn't override existing values",
		"vmType: vz",
		"{arch: aarch64, vmType: qemu}",
		"{arch: aarch64, vmType: vz}",
	},
	{
		"Comments are copied over as well",
		`#
# VM Type is QEMU
vmType: qemu # QEMU
`,
		`
# Arch is x86_64
arch: x86_64 # X86
`,
		`
# VM Type is QEMU
vmType: qemu  # QEMU
# Arch is x86_64
arch: x86_64  # X86
`,
	},
	{
		"mountTypesUnsupported are concatenated and duplicates removed",
		"mountTypesUnsupported: [9p,reverse-sshfs]",
		"mountTypesUnsupported: [9p,virtiofs]",
		"mountTypesUnsupported: [9p,reverse-sshfs,virtiofs]",
	},
	{
		"minimumLimaVersion (including comments) is updated when the base version is higher",
		`#
# Works with Lima 0.8.0 and later
minimumLimaVersion: 0.8.0 # needs 0.8.0
`,
		`
# Requires at least 1.0.2
minimumLimaVersion: 1.0.2    # or later
`,
		`
# Requires at least 1.0.2
minimumLimaVersion: 1.0.2  # or later
`,
	},
	{
		"vmOpts.qmu.minimumVersion is updated when the base version is higher",
		"vmOpts: {qemu: {minimumVersion: 8.2.1}}",
		"vmOpts: {qemu: {minimumVersion: 9.1.0}}",
		"vmOpts: {qemu: {minimumVersion: 9.1.0}}",
	},
	{
		"dns list is not appended, but the highest priority one is picked",
		"dns: [1.1.1.1]",
		"dns: [8.8.8.8, 1.2.3.4]",
		"dns: [1.1.1.1]",
	},
	{
		"Update comments on existing maps and lists that don't have comments yet",
		`#
additionalDisks:
- name: disk1 # One
`,
		`
# Mount additional disks
additionalDisks: # comment
# This is disk2
- name: disk2 # Two
`,
		`
# Mount additional disks
additionalDisks:  # comment
- name: disk1  # One
# This is disk2
- name: disk2  # Two
`,
	},
	{
		"probes and provision scripts are prepended instead of appended",
		"probes: [{script: 1}]\nprovision: [{script: One}]",
		"probes: [{script: 2}]\nprovision: [{script: Two}]",
		"probes: [{script: 2},{script: 1}]\nprovision: [{script: Two},{script: One}]",
	},
	{
		"additionalDisks append, but merge fields on shared name",
		"additionalDisks: [{name: disk1}]",
		"additionalDisks: [{name: disk2},{name: disk1, format: true}]",
		"additionalDisks: [{name: disk1, format: true},{name: disk2}]",
	},
	{
		// This test fails because there are 2 spurious newlines in the merged output
		"TODO mounts append, but merge fields on shared mountPoint",
		`#
# My mounts
mounts:
- location: loc1  # mountPoint loc1
- location: loc1
  mountPoint: loc2
`,
		`
mounts:
# will update mountPoint loc2
- location: loc1
  mountPoint: loc2
  writable: true
  # SSHFS
  sshfs:  # ssh
    followSymlinks: true
# will create new mountPoint loc3
- location: loc1
  mountPoint: loc3
  writable: true
`,
		`
# My mounts
mounts:
- location: loc1  # mountPoint loc1
# will update mountPoint loc2
- location: loc1
  mountPoint: loc2
  writable: true
  # SSHFS
  sshfs:  # ssh
    followSymlinks: true
# will create new mountPoint loc3
- location: loc1
  mountPoint: loc3
  writable: true
`,
	},
	{
		// This entry can be deleted when the previous one no longer fails
		"mounts append, but merge fields on shared mountPoint (no comments version)",
		`mounts: [{location: loc1}, {location: loc1, mountPoint: loc2}]`,
		`mounts: [{location: loc1, mountPoint: loc2, writable: true, sshfs: {followSymlinks: true}}, {location: loc1, mountPoint: loc3, writable: true}]`,
		`mounts: [{location: loc1}, {location: loc1, mountPoint: loc2, writable: true, sshfs: {followSymlinks: true}}, {location: loc1, mountPoint: loc3, writable: true}]`,
	},
	{
		"template:// URLs are not embedded when embedAll is false",
		// also tests file.url format
		``,
		`
base: template://default
provision:
- file:
    url: template://provision.sh
probes:
- file:
    url: template://probe.sh
`,
		`
base: template://default
provision:
- file: template://provision.sh
probes:
- file: template://probe.sh
`,
	},
	{
		"ERROR Each template must only be embedded once",
		`#
arch: aarch64
`,
		`
base: base0.yaml
# failure would mean this test loops forever, not that it fails the test
vmType: qemu
`,
		`base template loop detected`,
	},
	{
		"ERROR All bases following template:// bases must be template:// URLs too when embedAll is false",
		``,
		`base: [template://default, base1.yaml]`,
		"after not embedding",
	},
	{
		"ERROR All bases following template:// bases must be template:// URLs too when embedAll is false",
		``,
		`
base: [base1.yaml, base2.yaml]
---
base: template://default
---
base: baseX.yaml`,
		"after not embedding",
	},
	{
		"Bases are embedded depth-first",
		`#`,
		`
base: [base1.yaml, {url: base2.yaml}] # also test file.url format
additionalDisks: [disk0]
---
base: base3.yaml
additionalDisks: [disk1]
---
additionalDisks: [disk2]
---
additionalDisks: [disk3]
`,
		`
additionalDisks: [disk0, disk1, disk3, disk2]
`,
	},
	{
		"additionalDisks with name '*' are merged with all previous entries",
		`
additionalDisks:
- name: disk1
- name: disk2
- name: disk3
  format: false
`,
		`
additionalDisks:
- name: disk4
- name: "*"
  format: true # will apply to disk1, disk2, and disk4
- name: disk5
`,
		`
additionalDisks:
- name: disk1
  format: true
- name: disk2
  format: true
- name: disk3
  format: false
- name: disk4
  format: true
- name: disk5
`,
	},
	{
		// This test fails because the yq commands don't handle comments properly; may need to be fixed in yq
		"TODO additionalDisks will be upgraded from string to map",
		`#
additionalDisks:
# my head comment
- mine # my line comment
`,
		`
# head comment
additionalDisks: # line comment
- name: "*"
  format: true # formatting is good for you
`,
		`
# head comment
additionalDisks:  # line comment
# my head comment
- name: mine  # my line comment
  format: true  # formatting is good for you
`,
	},
	{
		// This entry can be deleted when the previous one no longer fails
		"additionalDisks will be upgraded from string to map (no comments version)",
		`additionalDisks: [mine]`,
		`additionalDisks: [{name: "*", format: true}]`,
		`additionalDisks: [{name: mine, format: true}]`,
	},
	{
		"networks without interface name are not merged",
		`
networks:
- interface: lima1
`,
		`
networks:
- interface: lima2
# The metric will not be merged with anything
- metric: 250
- interface: lima1
  metric: 100     # will be set on the first entry
- interface: '*'  # wildcard
  metric: 123     # will be set on the first entry
`,
		`
networks:
- interface: lima1
  metric: 100  # will be set on the first entry
- interface: lima2
  metric: 123  # will be set on the first entry
# The metric will not be merged with anything
- metric: 250
`,
	},
	{
		"Scripts are embedded with comments moved",
		`#
# Hi There!
provision:
# This script will be merged from an external file
- file: base1.sh # This comment will move to the "script" key
`,
		`
# base0.yaml is ignored
---
#!/usr/bin/env bash
echo "This is base1.sh"
`,
		`
# Hi There!
provision:
# This script will be merged from an external file
- script: |-  # This comment will move to the "script" key
    #!/usr/bin/env bash
    echo "This is base1.sh"
# base0.yaml is ignored
`,
	},
	{
		"Script files are embedded even when no base property exists",
		"provision: [{file: base0.sh}]",
		"#! my script",
		`provision: [{script: "#! my script"}]`,
	},
}

func TestEmbed(t *testing.T) {
	focus := os.Getenv("TEST_FOCUS")
	for _, tc := range embedTestCases {
		if focus != "" {
			if !strings.Contains(tc.description, focus) {
				continue
			}
			logrus.SetLevel(logrus.DebugLevel)
		}
		t.Run(tc.description, func(t *testing.T) { RunEmbedTest(t, tc) })
	}
	logrus.SetLevel(logrus.InfoLevel)
}

func RunEmbedTest(t *testing.T, tc embedTestCase) {
	todo := strings.HasPrefix(tc.description, "TODO")
	expectError := strings.HasPrefix(tc.description, "ERROR")
	stringCompare := strings.HasPrefix(tc.template, "#")

	// Normalize testcase data
	tc.template = strings.TrimSpace(strings.TrimPrefix(tc.template, "#"))
	tc.base = strings.TrimSpace(tc.base)
	tc.expected = strings.TrimSpace(tc.expected)

	// Change to temp directory so all template and script names don't include a slash.
	cwd, err := os.Getwd()
	assert.NilError(t, err, "Getting current working directory")
	err = os.Chdir(t.TempDir())
	assert.NilError(t, err, "Changing directory to t.TempDir()")
	defer func() {
		_ = os.Chdir(cwd)
	}()

	for i, base := range strings.Split(tc.base, "---\n") {
		extension := ".yaml"
		if strings.HasPrefix(base, "#!") {
			extension = ".sh"
		}
		baseFilename := fmt.Sprintf("base%d%s", i, extension)
		err := os.WriteFile(baseFilename, []byte(base), 0o600)
		assert.NilError(t, err, tc.description)
	}
	tmpl := &limatmpl.Template{
		Bytes:   []byte(fmt.Sprintf("base: base0.yaml\n%s", tc.template)),
		Locator: "tmpl.yaml",
	}
	// Don't include `base` property if base0 is a script
	if strings.HasPrefix(tc.base, "#!") {
		tmpl.Bytes = []byte(tc.template)
	}
	err = tmpl.Embed(context.TODO(), false, false)
	if expectError {
		assert.ErrorContains(t, err, tc.expected, tc.description)
		return
	}
	assert.NilError(t, err, tc.description)

	if stringCompare {
		actual := strings.TrimSpace(string(tmpl.Bytes))
		if todo {
			assert.Assert(t, actual != tc.expected, tc.description)
		} else {
			assert.Equal(t, actual, tc.expected, tc.description)
		}
		return
	}

	err = tmpl.Unmarshal()
	assert.NilError(t, err, tc.description)

	var expected limayaml.LimaYAML
	err = limayaml.Unmarshal([]byte(tc.expected), &expected, "expected")
	assert.NilError(t, err, tc.description)

	if todo {
		// using reflect.DeepEqual because cmp.DeepEqual can't easily be negated
		assert.Assert(t, !reflect.DeepEqual(tmpl.Config, &expected), tc.description)
	} else {
		assert.Assert(t, cmp.DeepEqual(tmpl.Config, &expected), tc.description)
	}
}
