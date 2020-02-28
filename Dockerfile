FROM golang:latest
RUN apt-get update &&\
    apt-get install -y libbtrfs-dev libdevmapper-dev
