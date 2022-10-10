# Splunk Redis+SignalFX Example

This example provides a `docker-compose` environment that sends redis data to stdout and sfx. You can change the exporters to your liking by modifying `otel-collector-config.yaml`.

You'll need to install docker at a minimum. Ensure the following environment variables are properly set:

1. `REDIS_PASSWORD` (default: `changeme`, see `redis.conf`)
1. `SPLUNK_ACCESS_TOKEN` (for sfx exporter)
1. `SPLUNK_REALM` (for sfx exporter)
1. `SPLUNK_PASSWORD` (for splunk exporter)

Once you've verified your environment, you can run the example by

```bash
$> docker-compose up --build
```

## Splunk Collector

Note that you may get an error similar to `{"kind": "exporter", "data_type": "metrics", "name": "splunk_hec/metrics", "error": "Post \"https://splunk_redis:8088/services/collector\": dial tcp 172.21.0.2:8088: connect: connection refused", "interval": "3.877023586s"}` upon first startup. This is likely because the splunk_hec receiver has not fully started yet. Wait until you see the hec receiver output something like `PLAY RECAP *********************************************************************`, and it should work as expected.
