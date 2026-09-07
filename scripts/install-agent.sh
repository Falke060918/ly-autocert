#!/bin/sh
# Install an already-built Linux agent. Enrollment is a separate one-time step.
set -eu
if [ "$(uname -s)" != Linux ] || [ "$(id -u)" != 0 ]; then
  echo 'Linux root 권한으로 실행하세요.' >&2
  exit 1
fi
if [ "$#" != 2 ]; then
  echo '사용법: sudo sh install-agent.sh /path/to/autocert-agent https://server:8443' >&2
  exit 1
fi
agent_binary=$1
agent_server=$2
case "$agent_server" in
  https://*) ;;
  *) echo 'HTTPS 서버 주소가 필요합니다.' >&2; exit 1 ;;
esac
case "$agent_server" in
  *[!a-zA-Z0-9.:/\[\]-]*) echo '서버 주소에 허용되지 않는 문자가 있습니다.' >&2; exit 1 ;;
esac
test -f "$agent_binary"
if [ -e /etc/systemd/system/autocert-agent.service ]; then
  echo '기존 서비스가 있습니다. 중지·백업 후 운영 매뉴얼의 업그레이드 절차를 따르세요.' >&2
  exit 1
fi
install -m 0755 "$agent_binary" /usr/local/bin/autocert-agent
install -d -m 0700 /var/lib/autocert-agent
cat > /etc/systemd/system/autocert-agent.service <<EOF
[Unit]
Description=AutoCert certificate deployment agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
UMask=0077
ExecStart=/usr/local/bin/autocert-agent run --server $agent_server --state-dir /var/lib/autocert-agent
Restart=on-failure
RestartSec=15
TimeoutStopSec=150

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
echo '설치 완료. 등록 토큰으로 enroll 후 systemctl enable --now autocert-agent를 실행하세요.'
