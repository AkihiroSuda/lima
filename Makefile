# Files are installed under $(DESTDIR)/$(PREFIX)
PREFIX ?= /usr/local
DEST := $(shell echo "$(DESTDIR)/$(PREFIX)" | sed 's:///*:/:g; s://*$$::')

GO ?= go
TAR ?= tar
ZIP ?= zip
PLANTUML ?= plantuml # may also be "java -jar plantuml.jar" if installed elsewhere

# The KCONFIG programs are only needed for re-generating the ".config" file.
# You can install the python "kconfiglib", if you don't have kconfig/kbuild.
KCONFIG_CONF ?= $(shell command -v kconfig-conf || command -v kbuild-conf || echo oldconfig)
KCONFIG_MCONF ?= $(shell command -v kconfig-mconf || command -v kbuild-mconf || echo menuconfig)

GOARCH ?= $(shell $(GO) env GOARCH)
GOHOSTARCH := $(shell $(GO) env GOHOSTARCH)
GOHOSTOS := $(shell $(GO) env GOHOSTOS)
GOOS ?= $(shell $(GO) env GOOS)
ifeq ($(GOOS),windows)
bat = .bat
exe = .exe
endif

GO_BUILDTAGS ?=
ifeq ($(GOOS),darwin)
MACOS_SDK_VERSION = $(shell xcrun --show-sdk-version | cut -d . -f 1)
ifeq ($(shell test $(MACOS_SDK_VERSION) -lt 13; echo $$?),0)
# The "vz" mode needs macOS 13 SDK or later
GO_BUILDTAGS += no_vz
endif
endif

ifeq ($(GOHOSTOS),windows)
WINVER_MAJOR=$(shell powershell.exe "[System.Environment]::OSVersion.Version.Major")
ifeq ($(WINVER_MAJOR),10)
WINVER_BUILD=$(shell powershell.exe "[System.Environment]::OSVersion.Version.Build")
WINVER_BUILD_HIGH_ENOUGH=$(shell powershell.exe $(WINVER_BUILD) -ge 19041)
ifeq ($(WINVER_BUILD_HIGH_ENOUGH),False)
GO_BUILDTAGS += no_wsl
endif
endif
endif

PACKAGE := github.com/lima-vm/lima

VERSION := $(shell git describe --match 'v[0-9]*' --dirty='.m' --always --tags)
VERSION_TRIMMED := $(VERSION:v%=%)

GO_BUILD_LDFLAGS := -ldflags="-s -w -X $(PACKAGE)/pkg/version.Version=$(VERSION)"
# `go -version -m` returns -tags with comma-separated list, because space-separated list is deprecated in go1.13.
# converting to comma-separated list is useful for comparing with the output of `go version -m`.
GO_BUILD_FLAG_TAGS := $(addprefix -tags=,$(shell echo "$(GO_BUILDTAGS)"|tr " " "\n"|paste -sd "," -))
GO_BUILD := $(GO) build $(GO_BUILD_LDFLAGS) $(GO_BUILD_FLAG_TAGS)

################################################################################
# Features
.NOTPARALLEL:
.SECONDEXPANSION:

################################################################################
.PHONY: all
all: binaries manpages

################################################################################
# Help
.PHONY: help
help:
	@echo  '  binaries        - Build all binaries'
	@echo  '  manpages        - Build manual pages'
	@echo
	@echo  "  Use 'make help-targets' to see additional targets."

.PHONY: help-targets
help-targets:
	@echo  '# Targets can be categorized by their location.'
	@echo
	@echo  'Targets for files in _output/bin/:'
	@echo  '- limactl                   : Build limactl, and lima'
	@echo  '- lima                      : Copy lima, and lima.bat'
	@echo  '- helpers                   : Copy nerdctl.lima, apptainer.lima, docker.lima, podman.lima, and kubectl.lima'
	@echo
	@echo  'Targets for files in _output/share/lima/:'
	@echo  '- guestagents               : Build guestagents for archs enabled by CONFIG_GUESTAGENT_ARCHS_*'
	@echo  '- native-guestagent         : Build guestagent for native arch'
	@echo  '- additional-guestagents    : Build guestagents for archs other than native arch'
	@echo  '- <arch>-guestagent         : Build guestagent for <arch>: $(sort $(GUESTAGENT_ARCHS))'
	@echo
	@echo  'Targets for files in _output/share/lima/templates/:'
	@echo  '- templates                 : Copy templates'
	@echo  '- template_experimentals    : Copy experimental templates to experimental/'
	@echo  '- default_template          : Copy default.yaml template'
	@echo
	@echo  'Targets for files in _output/share/doc/lima:'
	@echo  '- documentation             : Copy documentation to _output/share/doc/lima'
	@echo  '- create-links-in-doc-dir   : Create some symlinks pointing ../../lima/templates'
	@echo
	@echo  '# e.g. to install limactl, helpers, native guestagent, and templates:'
	@echo  '#   make native install'

.PHONY: help-artifact
help-artifact:
	@echo  '# Targets for building artifacts to _artifacts/'
	@echo
	@echo  'Targets to building multiple archs artifacts for GOOS:'
	@echo  '- artifacts                 : Build artifacts for current OS and supported archs'
	@echo  '- artifacts-<GOOS>          : Build artifacts for supported archs and <GOOS>: darwin, linux, or windows'
	@echo
	@echo  'Targets to building GOOS and ARCH (GOARCH, or uname -m) specific artifacts:'
	@echo  '- artifact                  : Build artifacts for current GOOS and GOARCH'
	@echo  '- artifact-<GOOS>           : Build artifacts for current GOARCH and <GOOS>: darwin, linux, or windows'
	@echo  '- artifact-<ARCH>           : Build artifacts for current GOOS with <ARCH>: amd64, arm64, x86_64, or aarch64'
	@echo  '- artifact-<GOOS>-<ARCH>    : Build artifacts for <GOOS> and <ARCH>'
	@echo
	@echo  '# GOOS and GOARCH can be specified with make parameters or environment variables.'
	@echo  '# e.g. to build artifact for linux and arm64:'
	@echo  '#   make GOOS=linux GOARCH=arm64 artifact'
	@echo
	@echo  'Targets for miscellaneous artifacts:'
	@echo  '- artifacts-misc            : Build artifacts for go.mod, go.sum, and vendor'

################################################################################
# convenience targets
exe: _output/bin/limactl$(exe)

.PHONY: minimal native
minimal: clean limactl native-guestagent default_template
native: clean limactl helpers native-guestagent templates

################################################################################
# Kconfig
config: Kconfig
	$(KCONFIG_CONF) $<

menuconfig: Kconfig
	MENUCONFIG_STYLE=aquatic \
	$(KCONFIG_MCONF) $<

# Copy the default config, if not overridden locally
# This is done to avoid a dependency on KCONFIG tools
.config: config.mk
	cp $^ $@

-include .config

################################################################################
.PHONY: binaries
binaries: limactl helpers guestagents \
	templates template_experimentals \
	documentation create-links-in-doc-dir

################################################################################
# _output/bin
.PHONY: limactl lima helpers
limactl: _output/bin/limactl$(exe) lima

### Listing Dependencies

# returns a list of files expanded from $(1) excluding directories and files ending with '_test.go'.
find_files_excluding_dir_and_test = $(shell find $(1) ! -type d ! -name '*_test.go')
FILES_IN_PKG = $(call find_files_excluding_dir_and_test, ./pkg)

# returns a list of files which are dependencies for the command $(1).
dependencies_for_cmd = go.mod $(call find_files_excluding_dir_and_test, ./cmd/$(1)) $(FILES_IN_PKG)

### Force Building Targets

# returns GOVERSION, CGO*, GO*, -ldflags, and -tags build variables from the output of `go version -m $(1)`.
# When CGO_* variables are not set, they are not included in the output.
# Because the CGO_* variables are not set means that those values are default values,
# it can be assumed that those values are same if the GOVERSION is same.
# $(1): target binary
extract_build_vars = $(shell \
	($(GO) version -m $(1) 2>&- || echo $(1):) | \
	awk 'FNR==1{print "GOVERSION="$$2}$$2~/^(CGO|GO|-ldflags|-tags).*=.+$$/{sub("^.*"$$2,$$2); print $$0}' \
)

# a list of keys from the GO build variables to be used for calling `go env`.
# keys starting with '-' are excluded because `go env` does not support those keys.
# $(1): extracted build variables from the binary
keys_in_build_vars = $(filter-out -%,$(shell for i in $(1); do echo $${i%%=*}; done))

# a list of GO build variables to build the target binary.
# $(1): target binary. expecting ENVS_$(2) is set to use the environment variables for the target binary.
# $(2): key of the GO build variable to be used for calling `go env`.
go_build_vars = $(shell \
	$(ENVS_$(1)) $(GO) env $(2) | \
	awk '/ /{print "\""$$0"\""; next}{print}' | \
	for k in $(2); do read -r v && echo "$$k=$${v}"; done \
) $(GO_BUILD_LDFLAGS) $(GO_BUILD_FLAG_TAGS)

# returns the difference between $(1) and $(2).
diff = $(filter-out $(2),$(1))$(filter-out $(1),$(2))

# returns diff between the GO build variables in the binary $(1) and the building variables.
# $(1): target binary
# $(2): extracted GO build variables from the binary
compare_build_vars = $(call diff,$(call go_build_vars,$(1),$(call keys_in_build_vars,$(2))),$(2))

# returns "force" if the GO build variables in the binary $(1) is different from the building variables.
# $(1): target binary. expecting ENVS_$(1) is set to use the environment variables for the target binary.
force_build = $(if $(call compare_build_vars,$(1),$(call extract_build_vars,$(1))),force,)

# returns the file name without .gz extension. It also gunzips the file with .gz extension if exists.
# $(1): target file
gunzip_if_exists = $(shell f=$(1); f=$${f%.gz}; test -f "$${f}.gz" && (set -x; gunzip -f "$${f}.gz") ; echo "$${f}")

# call force_build with passing output of gunzip_if_exists as an argument.
# $(1): target file
force_build_with_gunzip = $(call force_build,$(call gunzip_if_exists,$(1)))

force: # placeholder for force build

################################################################################
# _output/bin/limactl$(exe)

# dependencies for limactl
LIMACTL_DEPS = $(call dependencies_for_cmd,limactl)
ifeq ($(GOOS),darwin)
LIMACTL_DEPS += vz.entitlements
endif

# environment variables for limactl. this variable is used for checking force build.
#
# The hostagent must be compiled with CGO_ENABLED=1 so that net.LookupIP() in the DNS server
# calls the native resolver library and not the simplistic version in the Go library.
ENVS__output/bin/limactl$(exe) = CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) CC=$(CC)

_output/bin/limactl$(exe): $(LIMACTL_DEPS) $$(call force_build,$$@)
# If the previous cross-compilation was for GOOS=windows, limactl.exe might still be present.
ifneq ($(GOOS),windows) #
	@rm -rf _output/bin/limactl.exe
else
	@rm -rf _output/bin/limactl
endif
	$(ENVS_$@) $(GO_BUILD) -o $@ ./cmd/limactl
ifeq ($(GOOS),darwin)
	codesign -f -v --entitlements vz.entitlements -s - $@
endif

LIMA_CMDS = $(sort lima lima$(bat)) # $(sort ...) deduplicates the list
LIMA_DEPS = $(addprefix _output/bin/,$(LIMA_CMDS))
lima: $(LIMA_DEPS)

HELPER_CMDS = nerdctl.lima apptainer.lima docker.lima podman.lima kubectl.lima
HELPERS_DEPS = $(addprefix _output/bin/,$(HELPER_CMDS))
helpers: $(HELPERS_DEPS)

_output/bin/%: ./cmd/% | _output/bin
	cp -a $< $@

MKDIR_TARGETS += _output/bin

################################################################################
# _output/share/lima/lima-guestagent
LINUX_GUESTAGENT_PATH_COMMON = _output/share/lima/lima-guestagent.Linux-

# How to add architecture specific guestagent:
# 1. Add the architecture to GUESTAGENT_ARCHS
# 2. Add ENVS_$(LINUX_GUESTAGENT_PATH_COMMON)<arch> to set GOOS, GOARCH, and other necessary environment variables
GUESTAGENT_ARCHS = aarch64 armv7l riscv64 x86_64

ALL_GUESTAGENTS_NOT_COMPRESSED = $(addprefix $(LINUX_GUESTAGENT_PATH_COMMON),$(GUESTAGENT_ARCHS))
ifeq ($(CONFIG_GUESTAGENT_COMPRESS),y)
$(info Guestagents are unzipped each time to check the build configuration; they may be gunzipped afterward.)
gz=.gz
endif

ALL_GUESTAGENTS = $(addsuffix $(gz),$(ALL_GUESTAGENTS_NOT_COMPRESSED))

# guestagent path for the given architectures. it may has .gz extension if CONFIG_GUESTAGENT_COMPRESS is enabled.
# $(1): list of architectures
guestaget_path = $(foreach arch,$(1),$(LINUX_GUESTAGENT_PATH_COMMON)$(arch)$(gz))

NATIVE_GUESTAGENT_ARCH = $(shell uname -m | sed -e s/arm64/aarch64/)
NATIVE_GUESTAGENT = $(call guestaget_path,$(NATIVE_GUESTAGENT_ARCH))
ADDITIONAL_GUESTAGENT_ARCHS = $(filter-out $(NATIVE_GUESTAGENT_ARCH),$(GUESTAGENT_ARCHS))
ADDITIONAL_GUESTAGENTS = $(call guestaget_path,$(ADDITIONAL_GUESTAGENT_ARCHS))

ifeq ($(CONFIG_GUESTAGENT_OS_LINUX),y)

# config_guestagent_arch returns expanded value of CONFIG_GUESTAGENT_ARCH_<arch>
# $(1): architecture
# CONFIG_GUESTAGENT_ARCH_<arch> naming convention: uppercase, remove '_'
config_guestagent_arch = $(filter y,$(CONFIG_GUESTAGENT_ARCH_$(shell echo $(1)|tr -d _|tr a-z A-Z)))

# guestagent_path_enabled_by_config returns the path to the guestagent binary for the given architecture,
# or an empty string if the CONFIG_GUESTAGENT_ARCH_<arch> is not set.
guestagent_path_enabled_by_config = $(if $(call config_guestagent_arch,$(1)),$(call guestaget_path,$(1)))

# apply CONFIG_GUESTAGENT_ARCH_*
GUESTAGENTS = $(foreach arch,$(GUESTAGENT_ARCHS),$(call guestagent_path_enabled_by_config,$(arch)))
endif

.PHONY: guestagents native-guestagent additional-guestagents
guestagents: $(GUESTAGENTS)
native-guestagent: $(NATIVE_GUESTAGENT)
additional-guestagents: $(ADDITIONAL_GUESTAGENTS)
%-guestagent:
	@[ "$(findstring $(*),$(GUESTAGENT_ARCHS))" == "$(*)" ] && make $(call guestaget_path,$*)

# environment variables for linx-guestagent. these variable are used for checking force build.
ENVS_$(LINUX_GUESTAGENT_PATH_COMMON)aarch64 = CGO_ENABLED=0 GOOS=linux GOARCH=arm64
ENVS_$(LINUX_GUESTAGENT_PATH_COMMON)armv7l = CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7
ENVS_$(LINUX_GUESTAGENT_PATH_COMMON)riscv64 = CGO_ENABLED=0 GOOS=linux GOARCH=riscv64
ENVS_$(LINUX_GUESTAGENT_PATH_COMMON)x86_64 = CGO_ENABLED=0 GOOS=linux GOARCH=amd64
$(ALL_GUESTAGENTS_NOT_COMPRESSED): $(call dependencies_for_cmd,lima-guestagent) $$(call force_build_with_gunzip,$$@) | _output/share/lima
	$(ENVS_$@) $(GO_BUILD) -o $@ ./cmd/lima-guestagent
	chmod 644 $@
$(LINUX_GUESTAGENT_PATH_COMMON)%.gz: $(LINUX_GUESTAGENT_PATH_COMMON)% $$(call force_build_with_gunzip,$$@)
	@set -x; gzip $<

MKDIR_TARGETS += _output/share/lima

################################################################################
# _output/share/lima/templates
TEMPLATES = $(addprefix _output/share/lima/templates/,$(filter-out experimental,$(notdir $(wildcard templates/*))))
TEMPLATE_EXPERIMENTALS = $(addprefix _output/share/lima/templates/experimental/,$(notdir $(wildcard templates/experimental/*)))

.PHONY: default_template templates template_experimentals
default_template: _output/share/lima/templates/default.yaml
templates: $(TEMPLATES)
template_experimentals: $(TEMPLATE_EXPERIMENTALS)

$(TEMPLATES): | _output/share/lima/templates
$(TEMPLATE_EXPERIMENTALS): | _output/share/lima/templates/experimental
MKDIR_TARGETS += _output/share/lima/templates _output/share/lima/templates/experimental

_output/share/lima/templates/%: templates/%
	cp -aL $< $@

# returns "force" if GOOS==windows, or GOOS!=windows and the file $(1) is not a symlink.
# $(1): target file
# On Windows, always copy to ensure the target has the same file as the source.
force_link = $(if $(filter windows,$(GOOS)),force,$(shell test ! -L $(1) && echo force))

################################################################################
# _output/share/doc/lima
DOCUMENTATION = $(addprefix _output/share/doc/lima/,$(wildcard *.md) LICENSE SECURITY.md VERSION)

.PHONY: documentation
documentation: $(DOCUMENTATION)

_output/share/doc/lima/SECURITY.md: | _output/share/doc/lima
	echo "Moved to https://github.com/lima-vm/.github/blob/main/SECURITY.md" > $@

_output/share/doc/lima/VERSION: | _output/share/doc/lima
	echo $(VERSION) > $@

_output/share/doc/lima/%: % | _output/share/doc/lima
	cp -aL $< $@

MKDIR_TARGETS += _output/share/doc/lima

.PHONY: create-links-in-doc-dir
create-links-in-doc-dir: _output/share/doc/lima/templates
_output/share/doc/lima/templates: _output/share/lima/templates $$(call force_link,$$@)
# remove the existing directory or symlink
	rm -rf $@
ifneq ($(GOOS),windows)
	ln -sf ../../lima/templates $@
else
# copy from templates built in build process
	cp -aL $< $@
endif

################################################################################
# returns difference between GOOS GOARCH and GOHOSTOS GOHOSTARCH.
cross_compiling = $(call diff,$(GOOS) $(GOARCH),$(GOHOSTOS) $(GOHOSTARCH))
# returns true if cross_compiling is empty.
native_compiling = $(if $(cross_compiling),,true)

################################################################################
# _output/share/man/man1
.PHONY: manpages
# Set limactl.1 as explicit dependency.
# It's uncertain how many manpages will be generated by `make`,
# because `limactl` generates them without corresponding source code for the manpages.
manpages: _output/share/man/man1/limactl.1
_output/share/man/man1/limactl.1: _output/bin/limactl$(exe)
	@mkdir -p _output/share/man/man1
ifeq ($(native_compiling),true)
# The manpages are generated by limactl, so the limactl binary must be native.
	$< generate-doc _output/share/man/man1 \
		--output _output --prefix $(PREFIX)
endif

################################################################################
.PHONY: docsy
# Set limactl.md as explicit dependency.
# It's uncertain how many docsy pages will be generated by `make`,
# because `limactl` generates them without corresponding source code for the docsy pages.
docsy: website/_output/docsy/limactl.md
website/_output/docsy/limactl.md: _output/bin/limactl$(exe)
	@mkdir -p website/_output/docsy
ifeq ($(native_compiling),true)
# The docs are generated by limactl, so the limactl binary must be native.
	$< generate-doc --type docsy website/_output/docsy \
		--output _output --prefix $(PREFIX)
endif

################################################################################
schema-limayaml.json: _output/bin/limactl$(exe)
ifeq ($(native_compiling),true)
	$< generate-jsonschema --schemafile $@ templates/default.yaml
endif

.PHONY: check-jsonschema
check-jsonschema: schema-limayaml.json
	check-jsonschema --schemafile $< templates/default.yaml

################################################################################
.PHONY: diagrams
diagrams: docs/lima-sequence-diagram.png
docs/lima-sequence-diagram.png: docs/images/lima-sequence-diagram.puml
	$(PLANTUML) ./docs/images/lima-sequence-diagram.puml

################################################################################
.PHONY: install
install: uninstall
	mkdir -p "$(DEST)"
	# Use tar rather than cp, for better symlink handling
	( cd _output && $(TAR) c * | $(TAR) -xv --no-same-owner -C "$(DEST)" )
	if [ "$(shell uname -s )" != "Linux" -a ! -e "$(DEST)/bin/nerdctl" ]; then ln -sf nerdctl.lima "$(DEST)/bin/nerdctl"; fi
	if [ "$(shell uname -s )" != "Linux" -a ! -e "$(DEST)/bin/apptainer" ]; then ln -sf apptainer.lima "$(DEST)/bin/apptainer"; fi

.PHONY: uninstall
uninstall:
	@test -f "$(DEST)/bin/lima" || echo "lima not found in $(DEST) prefix"
	rm -rf \
		"$(DEST)/bin/lima" \
		"$(DEST)/bin/lima$(bat)" \
		"$(DEST)/bin/limactl$(exe)" \
		"$(DEST)/bin/nerdctl.lima" \
		"$(DEST)/bin/apptainer.lima" \
		"$(DEST)/bin/docker.lima" \
		"$(DEST)/bin/podman.lima" \
		"$(DEST)/bin/kubectl.lima" \
		"$(DEST)/share/man/man1/lima.1" \
		"$(DEST)/share/man/man1/limactl"*".1" \
		"$(DEST)/share/lima" "$(DEST)/share/doc/lima"
	if [ "$$(readlink "$(DEST)/bin/nerdctl")" = "nerdctl.lima" ]; then rm "$(DEST)/bin/nerdctl"; fi
	if [ "$$(readlink "$(DEST)/bin/apptainer")" = "apptainer.lima" ]; then rm "$(DEST)/bin/apptainer"; fi

.PHONY: check-generated
check-generated:
	@test -z "$$(git status --short | grep ".pb.desc" | tee /dev/stderr)" || \
		((git diff $$(find . -name '*.pb.desc') | cat) && \
		(echo "Please run 'make generate' when making changes to proto files and check-in the generated file changes" && false))

.PHONY: lint
lint: check-generated
	golangci-lint run ./...
	yamllint .
	ls-lint
	find . -name '*.sh' ! -path "./.git/*" | xargs shellcheck
	find . -name '*.sh' ! -path "./.git/*" | xargs shfmt -s -d

.PHONY: clean
clean:
	rm -rf _output vendor

.PHONY: install-tools
install-tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2

.PHONY: generate
generate:
	go generate ./...

################################################################################
# _artifacts/lima-$(VERSION_TRIMMED)-$(ARTIFACT_OS)-$(ARTIFACT_UNAME_M)
.PHONY: artifact

# returns the capitalized string of $(1).
capitalize = $(shell echo "$(1)"|awk '{print toupper(substr($$0,1,1)) tolower(substr($$0,2))}')

# returns the architecture name converted from GOARCH to GNU coreutils uname -m.
to_uname_m = $(foreach arch,$(1),$(shell echo $(arch) | sed 's/amd64/x86_64/' | sed 's/arm64/aarch64/'))

ARTIFACT_FILE_EXTENSIONS := .tar.gz

ifeq ($(GOOS),darwin)
# returns the architecture name converted from GOARCH to macOS's uname -m.
to_uname_m = $(foreach arch,$(1),$(shell echo $(arch) | sed 's/amd64/x86_64/'))
else ifeq ($(GOOS),linux)
# CC is required for cross-compiling on Linux.
CC = gcc
else ifeq ($(GOOS),windows)
# artifact in zip format also provided for Windows.
ARTIFACT_FILE_EXTENSIONS += .zip
endif

# artifacts: artifacts-$(GOOS)
ARTIFACT_OS = $(call capitalize,$(GOOS))
ARTIFACT_UNAME_M = $(call to_uname_m,$(GOARCH))
ARTIFACT_PATH_COMMON = _artifacts/lima-$(VERSION_TRIMMED)-$(ARTIFACT_OS)-$(ARTIFACT_UNAME_M)

artifact: $(addprefix $(ARTIFACT_PATH_COMMON),$(ARTIFACT_FILE_EXTENSIONS))

ARTIFACT_DES =  _output/bin/limactl$(exe) $(LIMA_DEPS) $(HELPERS_DEPS) \
	$(ALL_GUESTAGENTS) \
	$(TEMPLATES) $(TEMPLATE_EXPERIMENTALS) \
	$(DOCUMENTATION) _output/share/doc/lima/templates \
	_output/share/man/man1/limactl.1

# file targets
$(ARTIFACT_PATH_COMMON).tar.gz: $(ARTIFACT_DES) | _artifacts
	$(TAR) -C _output/ --no-xattrs -czvf $@ ./

$(ARTIFACT_PATH_COMMON).zip: $(ARTIFACT_DES) | _artifacts
	cd _output && $(ZIP) -r ../$@ *

# generate manpages using native limactl.
manpages-using-native-limactl: GOOS = $(GOHOSTOS)
manpages-using-native-limactl: GOARCH = $(GOHOSTARCH)
manpages-using-native-limactl: manpages

# returns "manpages-using-native-limactl" if $(1) is not equal to $(GOHOSTOS).
# $(1): GOOS
generate_manpages_if_needed = $(if $(filter $(if $(1),$(1),$(GOOS)),$(GOHOSTOS)),,manpages-using-native-limactl)

# build native arch first, then build other archs.
artifact_goarchs = arm64 amd64
goarchs_native_and_others = $(GOHOSTARCH) $(filter-out $(GOHOSTARCH),$(artifact_goarchs))

# artifacts is artifact bundles for each OS.
# if target GOOS is native, build native arch first, generate manpages, then build other archs.
# if target GOOS is not native, build native limactl, generate manpages, then build the target GOOS with archs.
.PHONY: artifacts artifacts-darwin artifacts-linux artifacts-windows
artifacts: $$(addprefix artifact-$$(GOOS)-,$$(goarchs_native_and_others))
artifacts-darwin: $$(call generate_manpages_if_needed,darwin) $$(addprefix artifact-darwin-,$$(goarchs_native_and_others))
artifacts-linux: $$(call generate_manpages_if_needed,linux) $$(addprefix artifact-linux-,$$(goarchs_native_and_others))
artifacts-windows: $$(call generate_manpages_if_needed,windows) $$(addprefix artifact-windows-,$$(goarchs_native_and_others))

# set variables for artifact variant targets.
artifact-darwin% artifact-darwin: GOOS = darwin
artifact-linux% artifact-linux: GOOS = linux
artifact-windows% artifact-windows: GOOS = windows
artifact-%-amd64 artifact-%-x86_64 artifact-amd64 artifact-x86_64: GOARCH = amd64
artifact-%-arm64 artifact-%-aarch64 artifact-arm64 artifact-aarch64: GOARCH = arm64

# build cross arch binaries.
artifact-%: $$(call generate_manpages_if_needed)
	make artifact GOOS=$(GOOS) GOARCH=$(GOARCH)

.PHONY: artifacts-misc
artifacts-misc: | _artifacts
	go mod vendor
	$(TAR) --no-xattrs -czf _artifacts/lima-$(VERSION_TRIMMED)-go-mod-vendor.tar.gz go.mod go.sum vendor

MKDIR_TARGETS += _artifacts

################################################################################
# This target must be placed after any changes to the `MKDIR_TARGETS` variable.
# It seems that variable expansion in Makefile targets is not done recursively.
$(MKDIR_TARGETS):
	mkdir -p $@
