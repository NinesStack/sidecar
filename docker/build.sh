#!/bin/bash

die() {
	echo $1
	exit 1
}

file ./sidecar | grep "ELF.*LSB" || die "../sidecar is missing or not a Linux binary"
echo "Building..."
npm install --prefix ./ui
docker build -f ./docker/Dockerfile -t sidecar . || die "Failed to build"
