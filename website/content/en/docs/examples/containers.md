---
title: Containers
weight: 5
---

## Running containers
{{< tabpane text=true >}}

{{% tab header="containerd" %}}
{{< tabpane text=true >}}
{{% tab header="Rootless" %}}
```bash
lima nerdctl run -d --name nginx -p 127.0.0.1:8080:80 nginx:alpine
```

or

```bash
nerdctl.lima run -d --name nginx -p 127.0.0.1:8080:80 nginx:alpine
```
{{% /tab %}}
{{% tab header="Rootful" %}}
```bash
lima sudo systemctl enable --now containerd
lima sudo nerdctl run -d --name nginx -p 127.0.0.1:8080:80 nginx:alpine
```
{{% /tab %}}
{{< /tabpane >}}

- For the usage of containerd and nerdctl (contaiNERD ctl), visit <https://github.com/containerd/containerd>
and <https://github.com/containerd/nerdctl>.

- If you have installed Lima by `make install`, the `nerdctl.lima` command is also available as `nerdctl`.
  If you have installed Lima by `brew install lima`, you may make an alias (or a symlink) by yourself:
  `alias nerdctl=nerdctl.lima`

{{% /tab %}}

{{% tab header="Docker" %}}
{{< tabpane text=true >}}
{{% tab header="Rootless" %}}
```bash
limactl start template://docker
export DOCKER_HOST=$(limactl list docker --format 'unix://{{.Dir}}/sock/docker.sock')
docker run -d --name nginx -p 127.0.0.1:8080:80 nginx:alpine
```
{{% /tab %}}
{{% tab header="Rootful" %}}
TBD
{{% /tab %}}
{{< /tabpane >}}
{{% /tab %}}

{{% tab header="Podman" %}}
{{< tabpane text=true >}}
{{% tab header="Rootless" %}}
TBD
{{% /tab %}}
{{% tab header="Rootful" %}}
TBD
{{% /tab %}}
{{< /tabpane >}}
{{% /tab %}}


{{% tab header="Kubernetes" %}}
{{< tabpane text=true >}}
{{% tab header="kubeadm" %}}
```bash
limactl start template://k8s
export KUBECONFIG=$(limactl list k8s --format 'unix://{{.Dir}}/copied-from-guest/kubeconfig.yaml')
kubectl apply -f ...
```
{{% /tab %}}
{{% tab header="k3s" %}}
TBD
{{% /tab %}}
{{< /tabpane >}}
{{% /tab %}}

{{< /tabpane >}}

- <http://127.0.0.1:8080> is accessible from the host, as well as from the VM.

## Accelerating pulls with eStargz

WIP

```console

$ time nerdctl --snapshotter=overlayfs run -it --rm ghcr.io/stargz-containers/python:3.7-org python3 -c 'print("hi")'
[...]
hi

real	0m33.505s
user	0m2.962s
sys	0m6.629s
```

```console
$ time nerdctl --snapshotter=stargz run -it --rm ghcr.io/stargz-containers/python:3.7-esgz python3 -c 'print("hi")'
[...]
hi

real	0m12.335s
user	0m0.469s
sys	0m0.522s
```
