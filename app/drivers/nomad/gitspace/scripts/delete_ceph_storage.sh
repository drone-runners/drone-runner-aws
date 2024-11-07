#!/usr/bin/bash

# Variables
RBD={{ .RBDIdentifier }}
POOL={{ .CephPoolIdentifier }}
MOUNT_POINT="/${RBD}"
RBD_DEVICE="/dev/rbd/${POOL}/${RBD}"

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
if rbd showmapped | grep -q "${POOL}.*${RBD}"; then
    echo "Unmapping RBD image ${POOL}/${RBD}..."
    sudo rbd unmap "${RBD_DEVICE}"
    echo "RBD image unmapped."
else
    echo "RBD image ${POOL}/${RBD} is not mapped."
fi

# Check if the RBD image exists and remove it
if rbd ls "${POOL}" | grep -q "${RBD}"; then
    echo "Removing RBD image ${POOL}/${RBD}..."
    sudo rbd rm "${POOL}/${RBD}"
    echo "RBD image removed."
else
    echo "RBD image ${POOL}/${RBD} does not exist."
fi
