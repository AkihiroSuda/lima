package limatmpl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/coreos/go-semver/semver"
	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/lima/pkg/store/dirnames"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/lima-vm/lima/pkg/version/versionutil"
	"github.com/lima-vm/lima/pkg/yqutil"
	"github.com/sirupsen/logrus"
)

var warnBaseIsExperimental = sync.OnceFunc(func() {
	logrus.Warn("`base` is experimental")
})

var warnFileIsExperimental = sync.OnceFunc(func() {
	logrus.Warn("`provision[*].file` and `probes[*].file` are experimental")
})

// Embed will recursively resolve all "base" dependencies and update the
// template with the merged result. It also inlines all external provisioning
// and probe scripts.
func (tmpl *Template) Embed(ctx context.Context, embedAll, defaultBase bool) error {
	if err := tmpl.UseAbsLocators(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	err := tmpl.embedAllBases(ctx, embedAll, defaultBase, seen)
	// additionalDisks, mounts, and networks may combine entries based on a shared key
	// This must be done after **all** base templates have been merged, so that wildcard keys can match
	// against all earlier list entries, and not just against the direct parent template.
	if err == nil {
		err = tmpl.combineListEntries()
	}
	return tmpl.ClearOnError(err)
}

func (tmpl *Template) embedAllBases(ctx context.Context, embedAll, defaultBase bool, seen map[string]bool) error {
	logrus.Debugf("Embedding templates into %q", tmpl.Locator)
	if defaultBase {
		configDir, err := dirnames.LimaConfigDir()
		if err != nil {
			return err
		}
		defaultBaseFilename := filepath.Join(configDir, filenames.Base)
		if _, err := os.Stat(defaultBaseFilename); err == nil {
			// turn string into single element list
			// empty concatenation works around bug https://github.com/mikefarah/yq/issues/2269
			tmpl.expr.WriteString("| ($a.base | select(type == \"!!str\")) |= [\"\" + .]\n")
			tmpl.expr.WriteString("| ($a.base | select(type == \"!!map\")) |= [[] + .]\n")
			// prepend base template at the beginning of the list
			tmpl.expr.WriteString(fmt.Sprintf("| $a.base = [%q, $a.base[]]\n", defaultBaseFilename))
			if err := tmpl.evalExpr(); err != nil {
				return err
			}
		}
	}
	for {
		if err := tmpl.Unmarshal(); err != nil {
			return err
		}
		if len(tmpl.Config.Base) == 0 {
			break
		}
		baseLocator := tmpl.Config.Base[0]
		isTemplate, _ := SeemsTemplateURL(baseLocator.URL)
		if isTemplate && !embedAll {
			// Once we skip a template:// URL we can no longer embed any other base template
			for i := 1; i < len(tmpl.Config.Base); i++ {
				isTemplate, _ = SeemsTemplateURL(tmpl.Config.Base[i].URL)
				if !isTemplate {
					return fmt.Errorf("cannot embed template %q after not embedding %q", tmpl.Config.Base[i].URL, baseLocator.URL)
				}
			}
			break
			// TODO should we track embedding of template:// URLs so we can warn if we embed a non-template:// URL afterwards?
		}

		if seen[baseLocator.URL] {
			return fmt.Errorf("base template loop detected: template %q already included", baseLocator.URL)
		}
		seen[baseLocator.URL] = true

		// remove base[0] from template before merging
		if err := tmpl.embedBase(ctx, baseLocator, embedAll, seen); err != nil {
			return err
		}
	}
	if err := tmpl.embedAllScripts(ctx, embedAll); err != nil {
		return err
	}
	if len(tmpl.Bytes) > yBytesLimit {
		return fmt.Errorf("template %q embedding exceeded the size limit (%d bytes)", tmpl.Locator, yBytesLimit)
	}
	return nil
}

func (tmpl *Template) embedBase(ctx context.Context, baseLocator limayaml.LocatorWithDigest, embedAll bool, seen map[string]bool) error {
	warnBaseIsExperimental()
	logrus.Debugf("Embedding base %q in template %q", baseLocator.URL, tmpl.Locator)
	if err := tmpl.Unmarshal(); err != nil {
		return err
	}
	base, err := Read(ctx, "", baseLocator.URL)
	if err != nil {
		return err
	}
	if err := base.UseAbsLocators(); err != nil {
		return err
	}
	if err := base.embedAllBases(ctx, embedAll, false, seen); err != nil {
		return err
	}
	if err := tmpl.merge(base); err != nil {
		return err
	}
	if len(tmpl.Bytes) > yBytesLimit {
		return fmt.Errorf("template %q embedding exceeded the size limit (%d bytes)", tmpl.Locator, yBytesLimit)
	}
	return nil
}

// evalExprImpl evaluates tmpl.expr against one or more documents.
// Called by evalExpr() and embedAllScripts() for single documents and merge() for 2 documents.
func (tmpl *Template) evalExprImpl(prefix string, bytes []byte) error {
	var err error
	expr := prefix + tmpl.expr.String() + "| $a"
	tmpl.Bytes, err = yqutil.EvaluateExpression(expr, bytes)
	tmpl.Config = nil
	tmpl.expr.Reset()
	return tmpl.ClearOnError(err)
}

// evalExpr evaluates tmpl.expr against the tmpl.Bytes document.
func (tmpl *Template) evalExpr() error {
	var err error
	if tmpl.expr.Len() > 0 {
		// There is just a single document; $a and $b are the same
		singleDocument := "select(document_index == 0) as $a | $a as $b\n"
		err = tmpl.evalExprImpl(singleDocument, tmpl.Bytes)
	}
	return err
}

// merge merges the base template into tmpl.
func (tmpl *Template) merge(base *Template) error {
	if err := tmpl.mergeBase(base); err != nil {
		return tmpl.ClearOnError(err)
	}
	documents := fmt.Sprintf("%s\n---\n%s", string(tmpl.Bytes), string(base.Bytes))
	return tmpl.evalExprImpl(mergeDocuments, []byte(documents))
}

// mergeBase generates a yq script to merge the template with a base.
// Most of the merging is done generically by the mergeDocuments script below.
// Only thing left is to compare minimum version numbers and keep the highest version.
func (tmpl *Template) mergeBase(base *Template) error {
	if err := tmpl.Unmarshal(); err != nil {
		return err
	}
	if err := base.Unmarshal(); err != nil {
		return err
	}
	if tmpl.Config.MinimumLimaVersion != nil && base.Config.MinimumLimaVersion != nil {
		if versionutil.GreaterThan(*base.Config.MinimumLimaVersion, *tmpl.Config.MinimumLimaVersion) {
			const minimumLimaVersion = "minimumLimaVersion"
			tmpl.copyField(minimumLimaVersion, minimumLimaVersion)
		}
	}
	if tmpl.Config.VMOpts.QEMU.MinimumVersion != nil && base.Config.VMOpts.QEMU.MinimumVersion != nil {
		tmplVersion := *semver.New(*tmpl.Config.VMOpts.QEMU.MinimumVersion)
		baseVersion := *semver.New(*base.Config.VMOpts.QEMU.MinimumVersion)
		if tmplVersion.LessThan(baseVersion) {
			const minimumQEMUVersion = "vmOpts.qemu.minimumVersion"
			tmpl.copyField(minimumQEMUVersion, minimumQEMUVersion)
		}
	}
	return nil
}

// mergeDocuments copies over settings from the base that don't yet exist
// in the template, and to append lists from the base to template lists.
// Both head and line comments are copied over as well.
//
// It also handles these special cases:
// * dns lists are not merged and only copied when the template doesn't have any dns entries at all.
// * probes and provision scripts are appended in reverse order.
// * mountTypesUnsupported have duplicate values removed.
// * base is removed from the template.
const mergeDocuments = `
  select(document_index == 0) as $a
| select(document_index == 1) as $b

# $c will be mutilated to implement our own "merge only new fields" logic.
| $b as $c

# Delete the base that is being merged right now
| $a | select(.base | tag == "!!seq") | del(.base[0])
| $a | select(.base | (tag == "!!seq" and length == 0)) | del(.base)
| $a | select(.base | tag == "!!str") | del(.base)

# If $a.base is a list, then $b.base must be a list as well
# (note $b, not $c, because we merge lists from $b)
| $b | select((.base | tag == "!!str") and ($a.base | tag == "!!seq")) | .base = [ "" + .base ]

# Delete base DNS entries if the template list is not empty.
| $a | select(.dns) | del($b.dns, $c.dns)

# Mark all new list fields with a custom tag. This is needed to avoid appending
# newly copied lists to themselves again when we merge lists.
| $c | .. | select(tag == "!!seq") tag = "!!tag"

# Delete all nodes in $c that are in $a and not a map. This is necessary because
# the yq "*n" operator (merge only new fields) does not copy all comments across.
| $c | delpaths([$a | .. | select(tag != "!!map") | path])

# Merging with null returns null; use an empty map if $c has only comments
| $a * ($c // {}) as $a

# Find all elements that are existing lists. This will not match newly
# copied lists because they have a custom !!tag instead of !!seq.
# Append the elements from the same path in $b.
# Exception: base templates, provision scripts and probes are prepended instead.
| $a | (.. | select(tag == "!!seq" and (path[0] | test("^(base|provision|probes)$") | not))) |=
   (. + (path[] as $p ireduce ($b; .[$p])))
| $a | (.. | select(tag == "!!seq" and (path[0] | test("^(base|provision|probes)$")))) |=
   ((path[] as $p ireduce ($b; .[$p])) + .)

# Copy head and line comments for existing lists that do not already have comments.
# New lists and existing maps already have their comments updated by the $a * $c merge.
| $a | (.. | select(tag == "!!seq" and (key | head_comment == "")) | key) head_comment |=
   (((path[] as $p ireduce ($b; .[$p])) | key | head_comment) // "")
| $a | (.. | select(tag == "!!seq" and (key | line_comment == "")) | key) line_comment |=
   (((path[] as $p ireduce ($b; .[$p])) | key | line_comment) // "")

# Make sure mountTypesUnsupported elements are unique.
| $a | (select(.mountTypesUnsupported) | .mountTypesUnsupported) |= unique

# Remove the custom tags again so they do not clutter up the YAML output.
| $a | .. tag = ""
`

// listFields returns dst and src fields like "list[idx].field".
func listFields(list string, dstIdx, srcIdx int, field string) (dst, src string) {
	dst = fmt.Sprintf("%s[%d]", list, dstIdx)
	src = fmt.Sprintf("%s[%d]", list, srcIdx)
	if field != "" {
		dst += "." + field
		src += "." + field
	}
	return dst, src
}

// copyField copies value and comments from $b.src to $a.dst.
func (tmpl *Template) copyField(dst, src string) {
	tmpl.expr.WriteString(fmt.Sprintf("| ($a.%s) = $b.%s\n", dst, src))
	// The head_comment is on the key and not the value, so needs to be copied explicitly.
	// Surprisingly the line_comment seems to be copied with the value already even though it is also on the key.
	tmpl.expr.WriteString(fmt.Sprintf("| ($a.%s | key) head_comment = ($b.%s | key | head_comment)\n", dst, src))
}

// copyListEntryField copies $b.list[srcIdx].field to $a.list[dstIdx].field (including comments).
// Note: field must not be "" and must not be a list field itself either.
func (tmpl *Template) copyListEntryField(list string, dstIdx, srcIdx int, field string) {
	tmpl.copyField(listFields(list, dstIdx, srcIdx, field))
}

type commentType string

const (
	headComment commentType = "head"
	lineComment commentType = "line"
)

// copyComment copies a non-empty head or line comment from $b.src to $a.dst, but only if $a.dst already exists.
func (tmpl *Template) copyComment(dst, src string, commentType commentType, isMapElement bool) {
	onKey := ""
	if isMapElement {
		onKey = " | key" // For map elements the comments are on the keys and not the values.
	}
	// The expression is careful not to create a null $a.dst entry if $b.src has no comments and $a.dst didn't already exist.
	// e.g.: `| $a | (select(.foo) | .foo | key | select(head_comment == "" and ($b.bar | key | head_comment != ""))) head_comment |= ($b.bar | key | head_comment)`
	tmpl.expr.WriteString(fmt.Sprintf("| $a | (select(.%s) | .%s%s | select(%s_comment == \"\" and ($b.%s%s | %s_comment != \"\"))) %s_comment |= ($b.%s%s | %s_comment)\n",
		dst, dst, onKey, commentType, src, onKey, commentType, commentType, src, onKey, commentType))
}

// copyComments copies all non-empty comments from $b.src to $a.dst.
func (tmpl *Template) copyComments(dst, src string, isMapElement bool) {
	for _, commentType := range []commentType{headComment, lineComment} {
		tmpl.copyComment(dst, src, commentType, isMapElement)
	}
}

// copyListEntryComments copies all non-empty comments from $b.list[srcIdx].field to $a.list[dstIdx].field.
func (tmpl *Template) copyListEntryComments(list string, dstIdx, srcIdx int, field string) {
	dst, src := listFields(list, dstIdx, srcIdx, field)
	isMapElement := field != ""
	tmpl.copyComments(dst, src, isMapElement)
}

func (tmpl *Template) deleteListEntry(list string, idx int) {
	tmpl.expr.WriteString(fmt.Sprintf("| del($a.%s[%d], $b.%s[%d])\n", list, idx, list, idx))
}

// upgradeListEntryStringToMapField turns list[idx] from a string to a {field: list[idx]} map.
func (tmpl *Template) upgradeListEntryStringToMapField(list string, idx int, field string) {
	// TODO the head_comment on the string becomes duplicated as a foot_comment on the new field; could be a yq bug?
	tmpl.expr.WriteString(fmt.Sprintf("| ($a.%s[%d] | select(type == \"!!str\")) |= {\"%s\": .}\n", list, idx, field))
}

// combineListEntries combines entries based on a shared unique key.
// If two entries share the same key, then any missing fields in the earlier entry are
// filled in from the latter one. The latter one is then deleted.
//
// Notes:
// * The field order is not maintained when entries with a matching key are merged.
// * The unique keys (and mount locations) are assumed to not be subject to Go templating.
// * A wildcard key '*' matches all prior list entries.
func (tmpl *Template) combineListEntries() error {
	if err := tmpl.Unmarshal(); err != nil {
		return err
	}

	tmpl.combineAdditionalDisks()
	tmpl.combineMounts()
	tmpl.combineNetworks()

	return tmpl.evalExpr()
}

// TODO: Maybe instead of hard-coding all the yaml names of LimaYAML struct fields we should
// TODO: retrieve them using reflection from the Go type tags to avoid possible typos.

// combineAdditionalDisks combines additionalDisks entries. The shared key is the disk name.
func (tmpl *Template) combineAdditionalDisks() {
	const additionalDisks = "additionalDisks"

	diskIdx := make(map[string]int, len(tmpl.Config.AdditionalDisks))
	for src := 0; src < len(tmpl.Config.AdditionalDisks); {
		disk := tmpl.Config.AdditionalDisks[src]
		var from, to int
		if disk.Name == "*" {
			// copy to **all** previous entries
			from = 0
			to = src - 1
		} else {
			if i, ok := diskIdx[disk.Name]; ok {
				// copy to previous disk with the same diskIdx
				from = i
				to = i
			} else {
				// record disk index and continue with the next entry
				if disk.Name != "" {
					diskIdx[disk.Name] = src
				}
				src++
				continue
			}
		}
		for dst := from; dst <= to; dst++ {
			// upgrade additionalDisks[dst] from "disk" name string to {"name": "disk"} map so we can add fields
			upgradeDiskToMap := sync.OnceFunc(func() {
				tmpl.upgradeListEntryStringToMapField(additionalDisks, dst, "name")
			})

			dest := &tmpl.Config.AdditionalDisks[dst]
			if dest.Format == nil && disk.Format != nil {
				upgradeDiskToMap()
				tmpl.copyListEntryField(additionalDisks, dst, src, "format")
				dest.Format = disk.Format
			}
			// TODO: Does it make sense to merge "fsType" and "fsArgs" independently of each other?
			if dest.FSType == nil && disk.FSType != nil {
				upgradeDiskToMap()
				tmpl.copyListEntryField(additionalDisks, dst, src, "fsType")
				dest.FSType = disk.FSType
			}
			// "fsArgs" are inherited all-or-nothing; they are not appended
			if len(dest.FSArgs) == 0 && len(disk.FSArgs) != 0 {
				upgradeDiskToMap()
				tmpl.copyListEntryField(additionalDisks, dst, src, "fsArgs")
				dest.FSArgs = disk.FSArgs
			}
			// TODO: Is there a good reason not to copy comments from wildcard entries?
			if disk.Name != "*" {
				tmpl.copyListEntryComments(additionalDisks, dst, src, "")
			}
		}
		tmpl.Config.AdditionalDisks = slices.Delete(tmpl.Config.AdditionalDisks, src, src+1)
		tmpl.deleteListEntry(additionalDisks, src)
	}
}

// combineMounts combines mounts entries. The shared key is the mount point.
func (tmpl *Template) combineMounts() {
	const mounts = "mounts"

	mountPointIdx := make(map[string]int, len(tmpl.Config.Mounts))
	for src := 0; src < len(tmpl.Config.Mounts); {
		mount := tmpl.Config.Mounts[src]
		// mountPoint (an optional field) defaults to location (a required field)
		mountPoint := mount.Location
		if mount.MountPoint != nil {
			mountPoint = *mount.MountPoint
		}
		var from, to int
		if mountPoint == "*" {
			from = 0
			to = src - 1
		} else {
			if i, ok := mountPointIdx[mountPoint]; ok {
				from = i
				to = i
			} else {
				if mountPoint != "" {
					mountPointIdx[mountPoint] = src
				}
				src++
				continue
			}
		}
		for dst := from; dst <= to; dst++ {
			dest := &tmpl.Config.Mounts[dst]
			// MountPoint
			if dest.MountPoint == nil && mount.MountPoint != nil {
				tmpl.copyListEntryField(mounts, dst, src, "mountPoint")
				dest.MountPoint = mount.MountPoint
			}
			// Writable
			if dest.Writable == nil && mount.Writable != nil {
				tmpl.copyListEntryField(mounts, dst, src, "writable")
				dest.Writable = mount.Writable
			}
			// SSHFS
			if dest.SSHFS.Cache == nil && mount.SSHFS.Cache != nil {
				tmpl.copyListEntryField(mounts, dst, src, "sshfs.cache")
				dest.SSHFS.Cache = mount.SSHFS.Cache
			}
			if dest.SSHFS.FollowSymlinks == nil && mount.SSHFS.FollowSymlinks != nil {
				tmpl.copyListEntryField(mounts, dst, src, "sshfs.followSymlinks")
				dest.SSHFS.FollowSymlinks = mount.SSHFS.FollowSymlinks
			}
			if dest.SSHFS.SFTPDriver == nil && mount.SSHFS.SFTPDriver != nil {
				tmpl.copyListEntryField(mounts, dst, src, "sshfs.sftpDriver")
				dest.SSHFS.SFTPDriver = mount.SSHFS.SFTPDriver
			}
			// NineP
			if dest.NineP.SecurityModel == nil && mount.NineP.SecurityModel != nil {
				tmpl.copyListEntryField(mounts, dst, src, "9p.securityModel")
				dest.NineP.SecurityModel = mount.NineP.SecurityModel
			}
			if dest.NineP.ProtocolVersion == nil && mount.NineP.ProtocolVersion != nil {
				tmpl.copyListEntryField(mounts, dst, src, "9p.protocolVersion")
				dest.NineP.ProtocolVersion = mount.NineP.ProtocolVersion
			}
			if dest.NineP.Msize == nil && mount.NineP.Msize != nil {
				tmpl.copyListEntryField(mounts, dst, src, "9p.msize")
				dest.NineP.Msize = mount.NineP.Msize
			}
			if dest.NineP.Cache == nil && mount.NineP.Cache != nil {
				tmpl.copyListEntryField(mounts, dst, src, "9p.cache")
				dest.NineP.Cache = mount.NineP.Cache
			}
			// Virtiofs
			if dest.Virtiofs.QueueSize == nil && mount.Virtiofs.QueueSize != nil {
				tmpl.copyListEntryField(mounts, dst, src, "virtiofs.queueSize")
				dest.Virtiofs.QueueSize = mount.Virtiofs.QueueSize
			}
			if mountPoint != "*" {
				tmpl.copyListEntryComments(mounts, dst, src, "")
				tmpl.copyListEntryComments(mounts, dst, src, "sshfs")
				tmpl.copyListEntryComments(mounts, dst, src, "9p")
				tmpl.copyListEntryComments(mounts, dst, src, "virtiofs")
			}
		}
		tmpl.Config.Mounts = slices.Delete(tmpl.Config.Mounts, src, src+1)
		tmpl.deleteListEntry(mounts, src)
	}
}

// combineNetworks combines networks entries. The shared key is the interface name.
func (tmpl *Template) combineNetworks() {
	const networks = "networks"

	interfaceIdx := make(map[string]int, len(tmpl.Config.Networks))
	for src := 0; src < len(tmpl.Config.Networks); {
		nw := tmpl.Config.Networks[src]
		var from, to int
		if nw.Interface == "*" {
			from = 0
			to = src - 1
		} else {
			if i, ok := interfaceIdx[nw.Interface]; ok {
				from = i
				to = i
			} else {
				if nw.Interface != "" {
					interfaceIdx[nw.Interface] = src
				}
				src++
				continue
			}
		}
		for dst := from; dst <= to; dst++ {
			dest := &tmpl.Config.Networks[dst]
			// Lima and Socket are mutually exclusive. Only copy base values if both are still unset.
			if dest.Lima == "" && dest.Socket == "" {
				if nw.Lima != "" {
					tmpl.copyListEntryField(networks, dst, src, "lima")
					dest.Lima = nw.Lima
				}
				if nw.Socket != "" {
					tmpl.copyListEntryField(networks, dst, src, "socket")
					dest.Socket = nw.Socket
				}
			}
			if dest.MACAddress == "" && nw.MACAddress != "" {
				tmpl.copyListEntryField(networks, dst, src, "macAddress")
				dest.MACAddress = nw.MACAddress
			}
			if dest.VZNAT == nil && nw.VZNAT != nil {
				tmpl.copyListEntryField(networks, dst, src, "vzNAT")
				dest.VZNAT = nw.VZNAT
			}
			if dest.Metric == nil && nw.Metric != nil {
				tmpl.copyListEntryField(networks, dst, src, "metric")
				dest.Metric = nw.Metric
			}
			if nw.Interface != "*" {
				tmpl.copyListEntryComments(networks, dst, src, "")
			}
		}
		tmpl.Config.Networks = slices.Delete(tmpl.Config.Networks, src, src+1)
		tmpl.deleteListEntry(networks, src)
	}
}

// updateScript replaces a "file" property with the actual script and then renames the field to "script".
func (tmpl *Template) updateScript(field string, idx int, script string) {
	entry := fmt.Sprintf("$a.%s[%d].file", field, idx)
	// Assign script to the "file" field and then rename it to "script".
	tmpl.expr.WriteString(fmt.Sprintf("| (%s) = %q | (%s | key) = \"script\"\n", entry, script, entry))
}

// embedAllScripts replaces all "provision" and "probes" file references with the actual script.
func (tmpl *Template) embedAllScripts(ctx context.Context, embedAll bool) error {
	if err := tmpl.Unmarshal(); err != nil {
		return err
	}
	for i, p := range tmpl.Config.Probes {
		// Don't overwrite existing script. This should throw an error during validation.
		if p.File != nil && p.Script == "" {
			warnFileIsExperimental()
			isTemplate, _ := SeemsTemplateURL(p.File.URL)
			if embedAll || !isTemplate {
				scriptTmpl, err := Read(ctx, "", p.File.URL)
				if err != nil {
					return err
				}
				tmpl.updateScript("probes", i, string(scriptTmpl.Bytes))
			}
		}
	}
	for i, p := range tmpl.Config.Provision {
		if p.File != nil && p.Script == "" {
			warnFileIsExperimental()
			isTemplate, _ := SeemsTemplateURL(p.File.URL)
			if embedAll || !isTemplate {
				scriptTmpl, err := Read(ctx, "", p.File.URL)
				if err != nil {
					return err
				}
				tmpl.updateScript("provision", i, string(scriptTmpl.Bytes))
			}
		}
	}
	return tmpl.evalExpr()
}
