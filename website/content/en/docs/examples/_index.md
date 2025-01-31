---
title: Examples
weight: 3
---

## Running Linux commands
```bash
lima sudo apt-get install -y neofetch
lima neofetch
```

## Accessing host files

By default, the VM has read-only accesses to `/Users/<USERNAME>` and read-write accesses to `/tmp/lima`.

To allow writing to `/Users/<USERNAME>`:
```bash
limactl edit --mount-writable --mount-type=virtiofs
```

Specifying `--mount-type=virtiofs` is not necessary here, but it is highly recommended
for the best performance and stability.

## Running containers

Lima comes with the built-in integration for [containerd](https://containerd.io) and
[nerdctl](https://github.com/containerd/nerdctl) (contaiNERD CTL):

```bash
nerdctl.lima run -d --name nginx -p 127.0.0.1:8080:80 nginx:alpine
```
<http://127.0.0.1:8080> is accessible from the host, as well as from the VM.

See also the [Containers](./containers) page for other examples such as using Docker and Kubernetes.

## Advanced configuration

```bash
limactl start \
  --name=default \
  --cpus=4 \
  --memory=8 \
  --vm-type=vz \
  --rosetta \
  --mount-type=virtiofs \
  --mount-writable \
  --network=vzNAT \
  template://fedora
```

- `--name=default`: Set the instance name to "default"
- `--cpus=4`: Set the number of the CPUs to 4
- `--memory=8`: Set the amount of the memory to 8 GiB
- `--vm-type=vz`: Use Apple's Virtualization.framework (vz) to enable Rosetta, virtiofs, and vzNAT
- `--rosetta`: Allow running Intel (AMD) binaries on ARM
- `--mount-type=virtiofs`: Use virtiofs for better performance
- `--mount-writable`: Make the home mount (`/Users/<USERNAME>`) writable
- `--network=vzNAT`: Make the VM reachable from the host by its IP address
- `template://fedora`: Use Fedora
