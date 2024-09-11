#!/usr/bin/bash

# Variables
RBD={{ .RBDIdentifier }}
POOL={{ .CephPoolIdentifier }}
MOUNT_POINT="/${RBD}"
RBD_DEVICE="/dev/rbd/${POOL}/${RBD}"

# Check if the RBD image exists
if ! rbd info ${POOL}/${RBD} &>/dev/null; then
    echo "Creating RBD image ${POOL}/${RBD}..."
    rbd create --size 8M ${POOL}/${RBD}
    # Indicate that the image has been created and needs formatting
    IMAGE_CREATED=true
else
    echo "RBD image '${RBD}' already exists in pool '${POOL}'."
    IMAGE_CREATED=false
fi

# Check if the RBD image is already mapped
if rbd showmapped | grep -q "${POOL}.*${RBD}"; then
    echo "RBD image ${POOL}/${RBD} is already mapped."
else
    echo "Mapping RBD image ${POOL}/${RBD}..."
    sudo rbd map ${POOL}/${RBD} --name client.admin
fi

# Format the RBD image only if it was newly created
if [ "${IMAGE_CREATED}" = true ]; then
    echo "Formatting RBD image ${POOL}/${RBD} with mkfs.ext4"
    sudo mkfs.ext4 ${RBD_DEVICE}
else
    echo "RBD image ${POOL}/${RBD} was not created, so no formatting is needed."
fi

# Mount the RBD image
if mount | grep -q "${MOUNT_POINT}"; then
    echo "RBD image is already mounted at ${MOUNT_POINT}."
else
    echo "Mounting RBD image ${POOL}/${RBD} to ${MOUNT_POINT}..."
    sudo mkdir -p "${MOUNT_POINT}"
    sudo mount "${RBD_DEVICE}" "${MOUNT_POINT}"
    echo "RBD image mounted successfully."
fi
