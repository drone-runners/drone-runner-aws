#!/usr/bin/bash

# Variables
POOL="harness-gitspaces-pool-1"
IMAGE="mygitspace-bcfq6b-1j3la8"
MOUNT_POINT="/mygitspace-bcfq6b-1j3la8"
RBD_DEVICE="/dev/rbd/${POOL}/${IMAGE}"

# Check if the RBD image is mounted and unmount it
if mount | grep -q "${MOUNT_POINT}"; then
    echo "Unmounting ${MOUNT_POINT}..."
    sudo umount "${MOUNT_POINT}"
    echo "Unmounted ${MOUNT_POINT}."
else
    echo "No mount found at ${MOUNT_POINT}."
fi

# Check if the mount directory exists and remove it
if [ -d "${MOUNT_POINT}" ]; then
    echo "Removing directory ${MOUNT_POINT}..."
    sudo rm -rf "${MOUNT_POINT}"
    echo "Directory ${MOUNT_POINT} removed."
else
    echo "Directory ${MOUNT_POINT} does not exist."
fi

# Check if the RBD image is mapped and unmap it
if rbd showmapped | grep -q "${POOL}.*${IMAGE}"; then
    echo "Unmapping RBD image ${POOL}/${IMAGE}..."
    sudo rbd unmap "${RBD_DEVICE}"
    echo "RBD image unmapped."
else
    echo "RBD image ${POOL}/${IMAGE} is not mapped."
fi
