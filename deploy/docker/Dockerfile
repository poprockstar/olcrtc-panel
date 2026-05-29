FROM debian:bookworm-slim

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates iproute2 iptables \
  && rm -rf /var/lib/apt/lists/*

COPY olcpanel /usr/local/bin/olcpanel

EXPOSE 8888
ENTRYPOINT ["/usr/local/bin/olcpanel"]
CMD ["serve", "--bind", "0.0.0.0:8888"]
