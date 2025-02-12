#!/bin/sh
# This script pretends that /bin/ash can be used as /bin/bash, so all following
# cloud-init scripts can use `#!/bin/bash` and `set -o pipefail`.
test -f /etc/alpine-release || exit 0

# Redirect bash to ash (built with CONFIG_ASH_BASH_COMPAT) and hope for the best :)
# It does support `set -o pipefail`, but not `[[`.
# /bin/bash can't be a symlink because /bin/ash is a symlink to /bin/busybox
cat >/bin/bash <<'EOF'
#!/bin/sh
exec /bin/ash "$@"
EOF
chmod +x /bin/bash
