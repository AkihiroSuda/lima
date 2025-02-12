---
title: Internal data structure
weight: 10
---

## Lima home directory (`${LIMA_HOME}`)

Defaults to `~/.lima`.

Note that we intentionally avoid using `~/Library/Application Support/Lima` on macOS.

We use `~/.lima` so that we can have enough space for the length of the socket path,
which must be less than 104 characters on macOS.

Unix: The directory can not be located on an NFS file system, it needs to be local.

### Config directory (`${LIMA_HOME}/_config`)

The config directory contains global lima settings that apply to all instances.

User identity:

Lima creates a default identity and uses its public key as the authorized key
to access all lima instances. In addition, lima will also configure all public
keys from `~/.ssh/*.pub` as well, so the user can use the ssh endpoint without
having to specify an identity explicitly.
- `user`: private key
- `user.pub`: public key

### Instance directory (`${LIMA_HOME}/<INSTANCE>`)

An instance directory contains the following files:

Metadata:
- `lima-version`: the Lima version used to create this instance
- `lima.yaml`: the YAML
- `protected`: empty file, used by `limactl protect`

cloud-init:
- `cloud-config.yaml`: cloud-init configuration, for reference only.
- `cidata.iso`: cloud-init ISO9660 image. See [`cidata.iso`](#cidataiso).

Ansible:
- `ansible-inventory.yaml`: the Ansible node inventory. See [ansible](#ansible).

disk:
- `basedisk`: the base image
- `diffdisk`: the diff image (QCOW2)

kernel:
- `kernel`: the kernel
- `kernel.cmdline`: the kernel cmdline
- `initrd`: the initrd

QEMU:
- `qemu.pid`: QEMU PID
- `qmp.sock`: QMP socket
- `qemu-efi-code.fd`: QEMU UEFI code (not always present)

VZ:
- `vz.pid`: VZ PID
- `vz-identifier`: Unique machine identifier file for a VM
- `vz-efi`: EFIVariable store file for a VM

Serial:
- `serial.log`: default serial log (QEMU only), for debugging
- `serial.sock`: default serial socket (QEMU only), for debugging (Usage: `socat -,echo=0,icanon=0 unix-connect:serial.sock`)
- `serialp.log`: PCI serial log (QEMU (ARM) only), for debugging
- `serialp.sock`: PCI serial socket (QEMU (ARM) only), for debugging (Usage: `socat -,echo=0,icanon=0 unix-connect:serialp.sock`)
- `serialv.log`: virtio serial log, for debugging
- `serialv.sock`: virtio serial socket (QEMU only), for debugging (Usage: `socat -,echo=0,icanon=0 unix-connect:serialv.sock`)

SSH:
- `ssh.sock`: SSH control master socket
- `ssh.config`: SSH config file for `ssh -F`. Not consumed by Lima itself.

VNC:
- `vncdisplay`: VNC display host/port
- `vncpassword`: VNC display password

Guest agent:

Each drivers use their own mode of communication
- `qemu`: uses virtio-port `io.lima-vm.guest_agent.0`
- `vz`: uses vsock port 2222
- `wsl2`: uses free random vsock port
The fallback is to use port forward over ssh port
- `ga.sock`: Forwarded to `/run/lima-guestagent.sock` in the guest, via SSH

Host agent:
- `ha.pid`: hostagent PID
- `ha.sock`: hostagent REST API
- `ha.stdout.log`: hostagent stdout (JSON lines, see `pkg/hostagent/events.Event`)
- `ha.stderr.log`: hostagent stderr (human-readable messages)

## Disk directory (`${LIMA_HOME}/_disk/<DISK>`)

A disk directory contains the following files:

data disk:
- `datadisk`: the qcow2 or raw disk that is attached to an instance

lock:
- `in_use_by`: symlink to the instance directory that is using the disk

When using `vmType: vz` (Virtualization.framework), on boot, any qcow2 (default) formatted disks that are specified in `additionalDisks` will be converted to RAW since [Virtualization.framework only supports mounting RAW disks](https://developer.apple.com/documentation/virtualization/vzdiskimagestoragedeviceattachment). This conversion enables additional disks to work with both Virtualization.framework and QEMU, but it has some consequences when it comes to interacting with the disks. Most importantly, a regular macOS default `cp` command will copy the _entire_ virtual disk size, instead of just the _used/allocated_ portion. The easiest way to copy only the used data is by adding the `-c` option to cp: `cp -c old_path new_path`. `cp -c` uses clonefile(2) to create a copy-on-write clone of the disk, and should return instantly.

`ls` will also only show the full/virtual size of the disks. To see the allocated space, `du -h disk_path` or `qemu-img info disk_path` can be used instead. See [#1405](https://github.com/lima-vm/lima/pull/1405) for more details.

## Lima cache directory (`~/Library/Caches/lima`)

Currently hard-coded to `~/Library/Caches/lima` on macOS.

Uses `$XDG_CACHE_HOME/lima`, normally `$HOME/.cache/lima`, on Linux.

Uses `%LocalAppData%\lima`, `C:\Users\<USERNAME>\AppData\Local\lima`, on Windows.

### Download cache (`~/Library/Caches/lima/download/by-url-sha256/<SHA256_OF_URL>`)

The directory contains the following files:

- `url`: raw url text, without "\n"
- `data`: data
- `<ALGO>.digest`: digest of the data, in OCI format.
   e.g., file name `sha256.digest`, with content `sha256:5ba3d476707d510fe3ca3928e9cda5d0b4ce527d42b343404c92d563f82ba967`

## Environment variables

- `$LIMA_HOME`: The "Lima home directory" (see above).
  - Default : `~/.lima`

- `$LIMA_INSTANCE`: `lima ...` is expanded to `limactl shell ${LIMA_INSTANCE} ...`.
  - Default : `default`

- `$LIMA_SHELL`: `lima ...` is expanded to `limactl shell --shell ${LIMA_SHELL} ...`.
  - No default : will use the user's shell configured inside the instance

- `$LIMA_WORKDIR`: `lima ...` is expanded to `limactl shell --workdir ${LIMA_WORKDIR} ...`.
  - No default : will attempt to use the current directory from the host

- `$QEMU_SYSTEM_X86_64`: path of `qemu-system-x86_64`
  - Default: `qemu-system-x86_64` in `$PATH`

- `$QEMU_SYSTEM_AARCH64`: path of `qemu-system-aarch64`
  - Default: `qemu-system-aarch64` in `$PATH`

- `$QEMU_SYSTEM_ARM`: path of `qemu-system-arm`
  - Default: `qemu-system-arm` in `$PATH`

## Ansible
The instance directory contains an inventory file, that might be used with Ansible playbooks and commands.
See [Building Ansible inventories](https://docs.ansible.com/ansible/latest/inventory_guide/) about dynamic inventories.

## `cidata.iso`
`cidata.iso` contains the following files:

- `user-data`: [Cloud-init user-data](https://docs.cloud-init.io/en/latest/explanation/format.html)
- `meta-data`: [Cloud-init meta-data](https://docs.cloud-init.io/en/latest/explanation/instancedata.html)
- `network-config`: [Cloud-init Networking Config Version 2](https://docs.cloud-init.io/en/latest/reference/network-config-format-v2.html)
- `lima.env`: The `LIMA_CIDATA_*` environment variables (see below) available during `boot.sh` processing
- `param.env`: The `PARAM_*` environment variables corresponding to the `param` settings from `lima.yaml`
- `lima-guestagent`: Lima guest agent binary
- `nerdctl-full.tgz`: [`nerdctl-full-<VERSION>-<OS>-<ARCH>.tar.gz`](https://github.com/containerd/nerdctl/releases)
- `boot.sh`: Boot script
- `boot.essential/*`: Boot script modules (executed in "plain" mode too)
- `boot/*`: Boot script modules
- `util/*`: Utility command scripts, executed in the boot script modules
- `provision.system/*`: Custom provision scripts (system)
- `provision.user/*`: Custom provision scripts (user)
- `etc_environment`: Environment variables to be added to `/etc/environment` (also loaded during `boot.sh`)

Max file name length = 30

### Volume label
The volume label is "cidata", as defined by [cloud-init NoCloud](https://docs.cloud-init.io/en/latest/reference/datasources/nocloud.html).

### Environment variables
- `LIMA_CIDATA_DEBUG`: the value of the `--debug` flag of the `limactl start` command.
- `LIMA_CIDATA_NAME`: the lima instance name
- `LIMA_CIDATA_MNT`: the mount point of the disk. `/mnt/lima-cidata`.
- `LIMA_CIDATA_USER`: the username string
- `LIMA_CIDATA_UID`: the numeric UID
- `LIMA_CIDATA_COMMENT`: the full name or comment string
- `LIMA_CIDATA_HOME`: the guest home directory
- `LIMA_CIDATA_SHELL`: the guest login shell
- `LIMA_CIDATA_HOSTHOME_MOUNTPOINT`: the mount point of the host home directory, or empty if not mounted
- `LIMA_CIDATA_MOUNTS`: the number of the Lima mounts
- `LIMA_CIDATA_MOUNTS_%d_MOUNTPOINT`: the N-th mount point of Lima mounts (N=0, 1, ...)
- `LIMA_CIDATA_MOUNTTYPE`: the type of the Lima mounts ("reverse-sshfs", "9p", ...)
- `LIMA_CIDATA_CONTAINERD_USER`: set to "1" if rootless containerd to be set up
- `LIMA_CIDATA_CONTAINERD_SYSTEM`: set to "1" if system-wide containerd to be set up
- `LIMA_CIDATA_CONTAINERD_ARCHIVE`: the name of the containerd archive. `nerdctl-full.tgz`
- `LIMA_CIDATA_SLIRP_GATEWAY`: set to the IP address of the host on the SLIRP network. `192.168.5.2`.
- `LIMA_CIDATA_SLIRP_DNS`: set to the IP address of the DNS on the SLIRP network. `192.168.5.3`.
- `LIMA_CIDATA_SLIRP_IP_ADDRESS`: set to the IP address of the guest on the SLIRP network. `192.168.5.15`.
- `LIMA_CIDATA_UDP_DNS_LOCAL_PORT`: set to the udp port number of the hostagent dns server (or 0 when not enabled).
- `LIMA_CIDATA_TCP_DNS_LOCAL_PORT`: set to the tcp port number of the hostagent dns server (or 0 when not enabled).

# VM lifecycle

![](/images/internals/lima-sequence-diagram.png)

(based on Lima 0.8.3)
