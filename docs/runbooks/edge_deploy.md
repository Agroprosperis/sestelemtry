# Runbook: деплой EMS edge на Siemens IOT2050

Едж-контролер `cmd/edge` (shadow-режим: читає SmartLogger, рахує
віртуальний диспатч, **нічого не пише** в регістри). Артефакти
деплою — у `deploy/edge/`, збірка — через `Makefile`.

Політика оновлень: версії pinned, rollout **ручний** (без watchtower і
авто-оновлень). Одна версія = один тег git → один tarball.

## 1. Артефакти

| Файл на пристрої | Звідки | Призначення |
| --- | --- | --- |
| `/opt/ems-edge/ems-edge` | `dist/ems-edge-linux-arm64` | статичний бінарник (CGO вимкнено) |
| `/etc/systemd/system/ems-edge.service` | `deploy/edge/ems-edge.service` | systemd-юніт, `Restart=always` |
| `/etc/ems-edge/config.yaml` | на базі `config.edge.example.yaml` | конфіг сайту (без секретів) |
| `/etc/ems-edge/edge.env` | на базі `deploy/edge/edge.env.example` | хости SL, URL хмари, Bearer-токен; `chmod 600` |
| `/etc/ems-edge/huawei_smartlogger.yaml` | `registers/huawei_smartlogger.yaml` | каталог регістрів |
| `/var/lib/ems-edge/` | створює systemd (`StateDirectory`) | blackbox SQLite + кеш manifest |

У `/etc/ems-edge/config.yaml` відносно прикладу міняються шляхи:

```yaml
register_catalog: /etc/ems-edge/huawei_smartlogger.yaml
blackbox:
  db_path: /var/lib/ems-edge/blackbox.db
manifest:
  cache_path: /var/lib/ems-edge/active_manifest.json
```

## 2. Передумови

1. **Хмара.** На ВМ в оточенні API задано токен сайту:
   `EDGE_SITE_TOKENS="ab=<token>"` (кілька сайтів — через кому). Після
   рестарту API в логах має бути `api_edge_ingest_enabled`.
2. **Мережа з пристрою.** IOT2050 бачить SmartLogger по `502/tcp` і
   хмару по HTTPS. Перевірка з пристрою:

```bash
nc -vz "$EDGE_SL_HOST" 502
curl -sS -o /dev/null -w '%{http_code}\n' "$EDGE_CLOUD_URL/healthz"
```

3. **Час.** `timedatectl` → NTP synchronized: yes (тики й звірка з
   PCAP залежать від коректного часу).
4. **Journald на диск** (щоб логи переживали ребут):

```bash
sudo mkdir -p /var/log/journal && sudo systemctl restart systemd-journald
```

## 3. Збірка й пакування (на робочій машині)

```bash
make edge-package
# → dist/ems-edge_<git-describe>_linux_arm64.tar.gz
```

Tarball містить бінарник, юніт, приклади env/config і каталог
регістрів. Версію бінарника перевіряють `ems-edge -version`.

## 4. Перший деплой

```bash
# 1. Скопіювати пакет на пристрій
scp dist/ems-edge_*_linux_arm64.tar.gz iot2050:/tmp/

# 2. На пристрої: розпакувати й розкласти
ssh iot2050
sudo useradd --system --home /var/lib/ems-edge --shell /usr/sbin/nologin ems-edge 2>/dev/null || true
mkdir -p /tmp/edge-pkg && tar -C /tmp/edge-pkg -xzf /tmp/ems-edge_*_linux_arm64.tar.gz

sudo install -d /opt/ems-edge /etc/ems-edge
sudo install -m 755 /tmp/edge-pkg/ems-edge /opt/ems-edge/ems-edge
sudo install -m 644 /tmp/edge-pkg/huawei_smartlogger.yaml /etc/ems-edge/
sudo install -m 644 /tmp/edge-pkg/config.edge.example.yaml /etc/ems-edge/config.yaml
sudo install -m 600 /tmp/edge-pkg/edge.env.example /etc/ems-edge/edge.env
sudo install -m 644 /tmp/edge-pkg/ems-edge.service /etc/systemd/system/

# 3. Відредагувати конфіг і env під сайт
sudoedit /etc/ems-edge/config.yaml   # site_id, топологія, шляхи з §1
sudoedit /etc/ems-edge/edge.env      # хости SL, EDGE_CLOUD_URL, токен

# 4. Запуск
sudo systemctl daemon-reload
sudo systemctl enable --now ems-edge
```

## 5. Перевірка після деплою

```bash
systemctl status ems-edge                      # active (running)
journalctl -u ems-edge -f                      # JSON-логи: edge_start, poll ok
sudo sqlite3 /var/lib/ems-edge/blackbox.db \
  "SELECT COUNT(*) FROM telemetry_raw; SELECT COUNT(*) FROM control_decisions;"
```

Локальна веб-консоль (стан, діагностика, manifest, emergency override):
`http://<ip-пристрою>:8081` — доступна тільки з LAN обʼєкта; порт і
вимкнення — секція `local_ui` конфіга.

На хмарі:

- `edge_heartbeats` має свіжий рядок сайту (heartbeat кожні 30 с);
- телеметрія приходить під org `<site>-edge` (суфікс шатл-фази,
  `EDGE_ORG_SUFFIX`);
- `control_decisions` наповнюється, `would_write_*` присутні,
  реальних записів у SmartLogger немає (mode=shadow це гарантує збіркою).

## 6. Логи й діагностика

```bash
journalctl -u ems-edge -f                        # хвіст
journalctl -u ems-edge --since -1h -o cat | jq . # JSON красиво
journalctl -u ems-edge | grep -E 'SL_POLL_FAIL|DISPATCH_DEGRADED|SHADOW_ANOMALY'
```

Черга на відвантаження (росте тільки в офлайні):

```bash
sudo sqlite3 /var/lib/ems-edge/blackbox.db \
  "SELECT 'telemetry', COUNT(*) FROM telemetry_raw WHERE uploaded=0
   UNION ALL SELECT 'decisions', COUNT(*) FROM control_decisions WHERE uploaded=0
   UNION ALL SELECT 'events', COUNT(*) FROM events WHERE uploaded=0;"
```

Актуальний manifest: `cat /var/lib/ems-edge/active_manifest.json | jq
'{manifest_id, mode, preset, valid_until}'`. Прострочений manifest —
очікуваний стан деградації: engine переходить на
`self_consumption_safe` і пише подію `DISPATCH_DEGRADED`.

## 7. Оновлення версії

```bash
make edge-package
scp dist/ems-edge_*_linux_arm64.tar.gz iot2050:/tmp/
ssh iot2050
tar -C /tmp -xzf /tmp/ems-edge_*_linux_arm64.tar.gz ./ems-edge
sudo cp /opt/ems-edge/ems-edge /opt/ems-edge/ems-edge.prev   # для відкату
sudo install -m 755 /tmp/ems-edge /opt/ems-edge/ems-edge
sudo systemctl restart ems-edge
/opt/ems-edge/ems-edge -version && journalctl -u ems-edge -n 20
```

Blackbox і кеш manifest живуть у `/var/lib/ems-edge` і оновлення
переживають; черга `uploaded=0` після рестарту довантажується сама.

## 8. Відкат

```bash
sudo cp /opt/ems-edge/ems-edge.prev /opt/ems-edge/ems-edge
sudo systemctl restart ems-edge
```

Схема blackbox створюється ідемпотентно (`CREATE TABLE IF NOT
EXISTS`), тому відкат на попередню версію безпечний для даних.

## 9. Recovery

- **Сервіс у рестарт-циклі.** `journalctl -u ems-edge -n 50`: майже
  завжди це конфіг (`edge_config`) або відсутній env. Юніт з
  `StartLimitIntervalSec=0` ретраїть вічно — після виправлення нічого
  скидати не треба.
- **Пошкоджений blackbox** (`database disk image is malformed`):

```bash
sudo systemctl stop ems-edge
sudo mv /var/lib/ems-edge/blackbox.db /var/lib/ems-edge/blackbox.db.corrupt.$(date +%s)
sudo systemctl start ems-edge     # схема створиться заново
```

  Невивантажений залишок можна пізніше дістати з `.corrupt.*` через
  `sqlite3 .recover`.
- **Повний диск.** Ретеншн сам чистить (30 днів; при ≥95 % диска —
  видалення uploaded oldest-first). Якщо диск забив хтось інший —
  звільнити місце і перезапустити сервіс.
- **Довгий офлайн хмари.** Нічого не робити: uplink ретраїть з
  backoff, blackbox накопичує. Ємності 30 днів вистачає з запасом.

## 9а. Пілот ze — фактичні параметри (2026-08-25)

Перевірені на живому розгортанні значення, які відрізняються від
прикладів вище:

- **unit_id: 99** для обох SmartLogger-ів (вендорська документація
  каже 0, але вся інсталяція ze/ab працює на 99 — звіряйте з
  `config.yaml` колектора на ВМ);
- **`ess_discharge_sign: -1`** для ze (прошивка звітує заряд додатним);
- **PV-логер (10.28.40.101) має Modbus TCP-обмеження клієнтів**:
  нову IP контролера треба додати у веб-інтерфейсі SmartLogger
  (Налаштування → Комунікації → Modbus TCP), інакше «connection reset
  by peer» одразу після запиту. ESS-логер (.102) пускав без змін;
- **мережа майданчик→сервер закрита** (ініціація лише ВМ→майданчик).
  Тимчасове рішення — реверс-SSH-тунель з ВМ, юніт
  `/etc/systemd/system/edge-tunnel-ze.service` (ssh -R
  18080:localhost:8080 root@10.28.40.88), а на пристрої
  `EDGE_CLOUD_URL=http://127.0.0.1:18080`. Постійне — заявка мережевику
  на TCP 8080 з підмережі майданчика до ВМ, після чого тунель
  прибирається;
- телеметрія shadow-фази в хмарі лежить під org **`ze-edge`**
  (EDGE_ORG_SUFFIX), рішення — у `control_decisions`.

## 10. Польові тести (spec §13)

### Тест A — паралельний poll ВМ vs IOT2050

RO-паралель двох Modbus-клієнтів підтверджена Photomate, перевіряємо
на ділі:

1. Едж запущений ≥ 1 години поруч зі штатним колектором ВМ.
2. На дашборді ВМ немає розривів/деградації графіків за цю годину
   (перевірити `data_quality` у семплах ВМ).
3. У журналі еджа немає сплеску `SL_POLL_FAIL`.
4. Порівняти криві `active_ess_power_kw` org `<site>` vs `<site>-edge`
   за годину — форма має збігатися (обидва потоки йдуть з кадансом 1 с).

**Критерій:** дашборди ВМ не деградують (sign-off №1).

### Тест B — офлайн 30 хв і догрузка backlog

1. Зафіксувати лічильники `uploaded=0` (SQL з §6).
2. Розірвати WAN (або заблокувати хмару):
   `sudo iptables -A OUTPUT -d <cloud-ip> -j DROP`
3. 30 хв: сервіс живий, `telemetry_raw uploaded=0` росте (~60 рядків/хв
   на метрику), рішення продовжують писатись, у журналі — retry uplink.
4. Відновити мережу: `sudo iptables -D OUTPUT -d <cloud-ip> -j DROP`
5. Черга `uploaded=0` спадає до ~0 (батчі по 600 записів кожні 30 с —
   30-хвилинний backlog догружається за кілька хвилин); на хмарі немає
   дублікатів (ідемпотентність по `batch_id`), у `telemetry_samples`
   немає дірки за вікно офлайну.

**Критерій:** повна догрузка без дублікатів і без дірок.

### Тест C — soak 7 діб

1. Залишити сервіс на ≥ 7 діб.
2. Щодня (або скриптом): heartbeat свіжий; рестартів немає
   (`systemctl show ems-edge -p NRestarts`); диск під контролем
   (`du -h /var/lib/ems-edge`; WAL не росте необмежено).
3. Наприкінці: покриття тиків за кожну добу ≥ 99 %
   (`SELECT date(ts_utc), COUNT(*) FROM telemetry_raw GROUP BY 1` —
   порівняти з теоретичними 86400/добу на метрику з поправкою на
   `poll_interval`), ретеншн відпрацьовує (немає рядків старше 30 діб).

**Критерій:** blackbox ≥ 7 діб безперервного запису (sign-off №2).

### Sign-off (spec §13.3)

1. Дашборди ВМ не деградують при паралельному poll — тест A.
2. Black box ≥ 7 діб безперервного запису — тест C.
3. Shadow decisions генеруються без жодного write у SL — збірка не
   містить коду запису; підтвердити відсутністю FC6/FC16 у трафіку
   еджа (за потреби `tcpdump -i any port 502 -w edge_modbus.pcap`).
4. Reconcile-звіт по `40381` (ze, 3–5 червня) з метриками — команда
   з розділу replay (`tools/shadow_vs_pcap_reconcile.py`).
