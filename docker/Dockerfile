FROM library/alpine:3.20

# Necessary depedencies
RUN apk --update add haproxy bash curl tar s6

# Set up Sidecar
ADD sidecar /sidecar/sidecar
ADD views /sidecar/views
ADD docker/s6 /etc
ADD ui /sidecar/ui

EXPOSE 7777

CMD ["/bin/s6-svscan", "/etc/s6"]
