# Reverse Proxy Notes

The one-line installer exposes OlcRTC Panel directly on
`http://SERVER_IP:PORT/OPTIONAL_URI` by setting `OLCPANEL_BIND=0.0.0.0:PORT`.
For TLS, bind the panel locally instead and terminate HTTPS in Caddy or Nginx.

Example `/etc/default/olcpanel` values for a reverse proxy:

```env
OLCPANEL_BIND=127.0.0.1:8888
OLCPANEL_BASE_PATH=/panel
```

## Caddy

```caddyfile
panel.example.com {
  handle_path /panel/* {
    reverse_proxy 127.0.0.1:8888
  }
  redir /panel /panel/
}
```

## Nginx

```nginx
server {
  listen 443 ssl http2;
  server_name panel.example.com;

  location = /panel {
    return 308 /panel/;
  }

  location /panel/ {
    proxy_pass http://127.0.0.1:8888/;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  }
}
```

Keep the panel's configured `OLCPANEL_BASE_PATH` aligned with the public path
used by the proxy.
