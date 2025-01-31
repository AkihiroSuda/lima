#!/bin/sh
test -f /etc/alpine-release || exit 0

# Make sure that root is in the sudoers file.
# This is needed to run the user provisioning scripts.
SUDOERS=/etc/sudoers.d/00-root-user
if [ ! -f $SUDOERS ]; then
	echo "root ALL=(ALL) NOPASSWD:ALL" >$SUDOERS
	chmod 660 $SUDOERS
fi

# Remove the user embedded in the image,
# and use cloud-init for users and groups.
if [ "$LIMA_CIDATA_USER" != "alpine" ]; then
	if [ "$(id -u alpine 2>&1)" = "1000" ]; then
		userdel alpine
		rmdir /home/alpine
		cloud-init clean --logs
		reboot
	fi
fi
