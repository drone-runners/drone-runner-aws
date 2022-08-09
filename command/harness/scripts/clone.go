package scripts

// Clone code pulled from: https://github.com/wings-software/drone-git/blob/master/posix/clone
const Clone = `
#!/bin/sh
# force the home directory path.

if [ "$HOME" != "/home/drone" ]; then
	if [ -d "/home/drone" ]; then
		echo "[DEBUG] setting default home directory"
		export HOME=/home/drone
	fi
fi

# if the netrc env variables exist, write
# the netrc file.

if [[ ! -z "${DRONE_NETRC_MACHINE}" ]]; then
	cat <<EOF > ${HOME}/.netrc
machine ${DRONE_NETRC_MACHINE}
login ${DRONE_NETRC_USERNAME}
password ${DRONE_NETRC_PASSWORD}
EOF
fi

# if the ssh_key environment variable exists, write
# the ssh key and add the netrc machine to the
# known hosts file.

if [[ ! -z "${DRONE_SSH_KEY}" ]]; then
	mkdir ${HOME}/.ssh
	echo "$DRONE_SSH_KEY" > ${HOME}/.ssh/id_rsa
	chmod 600 ${HOME}/.ssh/id_rsa

	touch ${HOME}/.ssh/known_hosts
	chmod 600 ${HOME}/.ssh/known_hosts

	SSH_KEYSCAN_FLAGS=""
	if [[ ! -z "${DRONE_NETRC_PORT}" ]]; then
		SSH_KEYSCAN_FLAGS="-p ${DRONE_NETRC_PORT}"
	fi
	ssh-keyscan -H ${SSH_KEYSCAN_FLAGS} ${DRONE_NETRC_MACHINE} > /etc/ssh/ssh_known_hosts 2> /dev/null

	export GIT_SSH_COMMAND="ssh -i ${HOME}/.ssh/id_rsa ${SSH_KEYSCAN_FLAGS} -F /dev/null"
fi

# AWS codecommit support using AWS access key & secret key
# Refer: https://docs.aws.amazon.com/codecommit/latest/userguide/setting-up-https-unixes.html

if [[ ! -z "$DRONE_AWS_ACCESS_KEY" ]]; then
	aws configure set aws_access_key_id $DRONE_AWS_ACCESS_KEY
	aws configure set aws_secret_access_key $DRONE_AWS_SECRET_KEY
	aws configure set default.region $DRONE_AWS_REGION

	git config --global credential.helper '!aws codecommit credential-helper $@'
	git config --global credential.UseHttpPath true
fi

# configure git global behavior and parameters via the
# following environment variables:


if [[ -z "${DRONE_COMMIT_AUTHOR_NAME}" ]]; then
	export DRONE_COMMIT_AUTHOR_NAME=drone
fi

if [[ -z "${DRONE_COMMIT_AUTHOR_EMAIL}" ]]; then
	export DRONE_COMMIT_AUTHOR_EMAIL=drone@localhost
fi

export GIT_AUTHOR_NAME=${DRONE_COMMIT_AUTHOR_NAME}
export GIT_AUTHOR_EMAIL=${DRONE_COMMIT_AUTHOR_EMAIL}
export GIT_COMMITTER_NAME=${DRONE_COMMIT_AUTHOR_NAME}
export GIT_COMMITTER_EMAIL=${DRONE_COMMIT_AUTHOR_EMAIL}

# invoke the sub-script based on the drone event type.
# TODO we should ultimately look at the ref, since
# we need something compatible with deployment events.

CLONE_TYPE=$DRONE_BUILD_EVENT
case $DRONE_COMMIT_REF in
  refs/tags/* ) CLONE_TYPE=tag ;;
  refs/pull/* ) CLONE_TYPE=pull_request ;;
  refs/pull-request/* ) CLONE_TYPE=pull_request ;;
  refs/merge-requests/* ) CLONE_TYPE=pull_request ;;
esac

case $CLONE_TYPE in
pull_request)
	FLAGS=""
if [[ ! -z "${PLUGIN_DEPTH}" ]]; then
	FLAGS="--depth=${PLUGIN_DEPTH}"
fi

if [ ! -d .git ]; then
	set -x
	git init
	git remote add origin ${DRONE_REMOTE_URL}
	set +x
fi

# If PR clone strategy is cloning only the source branch
if [ "$PLUGIN_PR_CLONE_STRATEGY" == "SourceBranch" ]; then
	set -e
	set -x

	git fetch ${FLAGS} origin ${DRONE_COMMIT_REF}:
	git checkout ${DRONE_COMMIT_SHA} -b ${DRONE_SOURCE_BRANCH}
	exit 0
fi

# PR clone strategy is merge commit

targetRef=${DRONE_COMMIT_BRANCH}
if [[ ! -z "${DRONE_COMMIT_BEFORE}" ]]; then
	targetRef="${DRONE_COMMIT_BEFORE} -b ${DRONE_COMMIT_BRANCH}"
fi


set -e
set -x

git fetch ${FLAGS} origin +refs/heads/${DRONE_COMMIT_BRANCH}:
git checkout ${targetRef}

git fetch origin ${DRONE_COMMIT_REF}:
git merge ${DRONE_COMMIT_SHA}
	;;
tag)
	FLAGS=""
if [[ ! -z "${PLUGIN_DEPTH}" ]]; then
	FLAGS="--depth=${PLUGIN_DEPTH}"
fi

if [ ! -d .git ]; then
	set -x
	git init
	git remote add origin ${DRONE_REMOTE_URL}
	set +x
fi

set -e
set -x

git fetch ${FLAGS} origin +refs/tags/${DRONE_TAG}:
git checkout -qf FETCH_HEAD
	;;
*)
	FLAGS=""
if [[ ! -z "${PLUGIN_DEPTH}" ]]; then
	FLAGS="--depth=${PLUGIN_DEPTH}"
fi

if [ ! -d .git ]; then
	set -x
	git init
	git remote add origin ${DRONE_REMOTE_URL}
	set +x
fi

# the branch may be empty for certain event types,
# such as github deployment events. If the branch
# is empty we checkout the sha directly. Note that
# we intentially omit depth flags to avoid failed
# clones due to lack of history.
if [[ -z "${DRONE_COMMIT_BRANCH}" ]]; then
	set -e
	set -x
	git fetch origin
	git checkout -qf ${DRONE_COMMIT_SHA}
	exit 0
fi

# the commit sha may be empty for builds that are
# manually triggered in Harness CI Enterprise. If
# the commit is empty we clone the branch.
if [[ -z "${DRONE_COMMIT_SHA}" ]]; then
	set -e
	set -x
	git fetch ${FLAGS} origin +refs/heads/${DRONE_COMMIT_BRANCH}:
	git checkout -b ${DRONE_COMMIT_BRANCH} origin/${DRONE_COMMIT_BRANCH}
	exit 0
fi

set -e
set -x

git fetch ${FLAGS} origin +refs/heads/${DRONE_COMMIT_BRANCH}:
git checkout ${DRONE_COMMIT_SHA} -b ${DRONE_COMMIT_BRANCH}
	;;
esac


`
