# План развития

Дорожная карта к стабильному mini-game серверу. Не контракт — пересматриваем
после каждого шага. Версия протокола: **763 (1.20.1)**. Stdlib only.

---

## Где мы сейчас

- Протокол 1.20.1 покрыт от handshake до play-state.
- 87 тестов (`go test -race ./...` чисто).
- Compression (zlib threshold 256), online-mode (Mojang auth + AES-CFB8),
  offline-mode + `banlist.json` loader.
- Два игрока видят друг друга: Player Info Update, Spawn Player, Teleport
  Entity, Remove Entities, Player Info Remove работают и проверены.
- Block Update broadcast'ится; pre-seed мира в `main.go` доходит до клиента.
- Регистрация/деспавн под одним `Server.joinMu` — никаких race'ов на
  visibility.

## Архитектурный поворот: mini-game сервер

Раньше план целился в «один большой мир». Теперь — **mini-game платформа**:
- Несколько изолированных **Instance**'ов (хаб, лобби, активные раунды игр).
- Игроки переходят между Instance'ами без disconnect от TCP.
- Каждая игра — **internal Go-плагин** (пакет в `games/`, регистрируется
  через `init()`). Конфиг включает/выключает по ID.

Это меняет приоритеты: view-distance culling не нужен (границы Instance'а
делают то же самое естественно), но нужны Instance, tick loop, перенос
игроков, шаблоны миров.

## Roadmap к стабильно работающему серверу

В порядке выполнения. Каждый пункт автономный — закончил, мердж, переходим
к следующему.

### 1. Load-test harness + первый pprof

**Зачем:** все цифры ниже — гипотезы. Без harness'а оптимизации вслепую.

- N горутин-«клиентов», каждая через `net.Pipe` логинится и держит
  соединение.
- Симуляция движения (20 Hz SetPos), чата (1/мин), периодический disconnect.
- Метрики: подключений/сек, latency броадкаста, CPU breakdown, GC pause.

**Стоимость:** 1–2 дня. До этого никаких других оптимизаций.

### 2. Per-connection send queue

**Зачем:** сейчас `safeWrite` синхронный. Один тормозящий клиент = весь
broadcast стоит 5 сек (write-deadline). Для хаба с сотнями игроков —
блокер.

- Канал `outbound chan []byte` на каждое `ClientConnection`.
- Dedicated writer goroutine читает из канала, пишет в `conn`.
- Backpressure: при переполнении канала — кикать клиента **или**
  coalescing'ом дропать устаревшие position-update'ы (хранить «последнее
  значение для entity X», а не очередь).
- `safeWrite` становится non-blocking push.

**Стоимость:** 2–3 дня. Тесты на медленного клиента, на coalescing.

### 3. Player race fix

**Зачем:** `c.player.X/Y/Z` сейчас пишет owning goroutine, читают
broadcast'ы из чужих. `-race` пока не словил, но проблема есть.

- `Player` получает `sync.RWMutex` (или атомики через `math.Float64bits`).
- Геттеры/сеттеры под Lock.

**Стоимость:** полдня. Чисто санитарная работа.

### 4. Instance abstraction

**Зачем:** фундамент для всего mini-game'ового.

```go
type Instance struct {
    ID      string
    World   world.World
    Players *PlayerList    // переезжает сюда из Server
    // hooks (см. шаг 6)
}
```

- `Server` теперь держит `Hub *Instance` + `Instances map[string]*Instance`.
- Все коннектящиеся попадают в Hub.
- `ClientConnection.instance *Instance` — текущий инстанс игрока.
- `Server.Players` остаётся для cross-instance операций, но визуальные
  broadcast'ы (Spawn, Position, Chat) идут через `instance.Players`.
- Player Info Update — per-instance: в tab list только текущая игра.

**Стоимость:** 3–5 дней. Большой рефакторинг handler'ов.

### 5. Tick loop per Instance

**Зачем:** базис для всего динамического — game state machine, движение
NPC, регенерация HP, периодические события.

- Один `time.Ticker(50ms)` на Instance (20Hz).
- `Instance.OnTick(fn func(tick uint64))` для подписки.
- Остановка через `Instance.Stop()` (нужно добавить lifecycle).

**Стоимость:** 1–2 дня.

### 5.5. Обработка интерактивных serverbound-пакетов

**Зачем:** клиент шлёт пакеты для PvP, анимации, постановки блоков, а
сервер их сейчас игнорирует (фоллбек в `default` с логом «Unknown play
packet»). Без них mini-game-логика не имеет смысла.

| Packet | ID | Содержит | Зачем нужен |
|---|---|---|---|
| Swing Arm | `0x2F` | VarInt(hand) | Анимация удара — broadcast как `Entity Animation (Cb 0x04)` |
| Interact | `0x10` | target_eid + action(attack/use) + опц. coords + hand + sneak | PvP-атака, использование сущности |
| Use Item On Block | `0x31` | hand + Position + face + cursor + sequence | Постановка блоков |
| Player Action | `0x1C` | action + Position + face + sequence | Ломание блоков (digging started/finished/cancel) |

- Добавить ID-константы в `packet_ids.go`.
- Парсеры в `handler_play.go` для каждого.
- Для Swing Arm — broadcast в Instance анимации.
- Для Interact (attack) — вызывать `Instance.OnPlayerDamage` (хук для игр).
- Для Use Item On + Player Action — обновлять `world.World` через
  `Server.SetBlock` (Block Update уже работает), но через хук
  `OnBlockBreak`/`OnBlockPlace` — чтобы игра могла отменить.

**Стоимость:** 2–3 дня. Без этого хук-API из шага 6 не на чем тестировать.

### 6. Event hooks в Instance

**Зачем:** игры должны реагировать на `BlockBreak`/`BlockPlace`/`Chat`/
`PlayerDeath`/`PlayerJoin`/`PlayerLeave` без правки ядра.

- Каждый хук — поле-функция на Instance: `Instance.OnBlockBreak func(...)
  bool` (вернуть false = отменить событие).
- В handler'ах (`handler_play.go`, `cleanup.go`) — вызовы этих хуков перед
  применением события к миру.
- Дефолтные значения: разрешено всё.

**Стоимость:** 2–3 дня.

### 7. Cross-instance teleport

**Зачем:** игрок перемещается hub → лобби → игра → hub без переподключения.

- `Server.MovePlayer(c, target *Instance)`:
  1. `announceLeave` в старом Instance.
  2. Очистить с клиента сущности старого Instance (Remove Entities).
  3. Сменить `c.instance = target`.
  4. Отправить **Respawn** packet (`Cb 0x41`) — клиент чистит мир.
  5. Отправить чанки нового мира.
  6. `announceJoin` в новом.
  7. Synchronize Player Position на spawn point.
- `Player.Reset()` — HP, инвентарь, gamemode из template'а Instance'а.

**Стоимость:** 3–5 дней. Самая хрупкая часть — тестировать с реальным
клиентом обязательно.

### 8. World templates + Clone

**Зачем:** каждая игра стартует с одинаковой ареной. Шаблон → копия на
раунд → удалить после.

- `world.Template` — read-only снапшот блоков + spawn points + metadata.
- `Template.Instantiate() *MemoryWorld` — клонирует в свежий World.
- Загрузка из памяти (хардкод-арена для тестов) или из файла — см. секцию
  «Парсинг карт» ниже.

**Стоимость:** 2 дня (без файл-парсера, только in-memory).

### 9. `game/` package — plugin API

**Зачем:** игры подключаются как пакеты, не правят ядро.

```go
// game/game.go
type Definition struct {
    ID         string
    Name       string
    MinPlayers int
    MaxPlayers int
    Template   *world.Template
    New        func() Logic     // фабрика runtime-логики на инстанс
}

type Logic interface {
    OnInstanceStart(ctx *Ctx)
    OnPlayerJoin(ctx *Ctx, p *player.Player)
    OnPlayerLeave(ctx *Ctx, p *player.Player)
    OnPlayerDeath(ctx *Ctx, p, killer *player.Player)
    OnTick(ctx *Ctx, tick uint64)
    OnBlockBreak(ctx *Ctx, p *player.Player, pos world.Position) bool
    OnBlockPlace(ctx *Ctx, p *player.Player, pos world.Position, b world.Block) bool
    OnChat(ctx *Ctx, p *player.Player, msg string) (rewrite string, allow bool)
    OnInstanceEnd(ctx *Ctx)
}

type NoopLogic struct{}  // embed-able defaults

func Register(*Definition)
func Get(id string) (*Definition, bool)
func All() []*Definition
```

`Ctx` — единственная точка для плагина общаться с движком:
`Broadcast`, `Teleport`, `SetBlock`, `EndGame`, `PlayersInRadius` и т.д.
**Не выставляется наружу:** `ClientConnection`, encryption, raw mutex'ы,
произвольное `go fn()`.

**Стоимость:** 1 день.

### 10. Matchmaker

**Зачем:** очереди игроков в игры, создание Instance'ов на запрос.

- `Server.Matchmaker.Queue(c, gameID)` — добавить в очередь.
- При достижении `MinPlayers`: создать Instance из Template, переместить
  ожидающих.
- При нехватке за `WaitSeconds` — кикнуть таймер или продолжить ждать
  (стратегия конфигурируется).
- Жизненный цикл Instance: `pending → starting → running → ending →
  cleanup`. State machine.

**Стоимость:** 2–3 дня.

### 11. KeepAlive timeout enforcement

**Зачем:** сейчас server шлёт keep-alive каждые 20с, но ответ не проверяет.
Зомби-соединения копятся.

- Хранить `outstandingKeepAliveID int64` + `sentAt time.Time` на
  `ClientConnection`.
- В `handlePlay` для `SbPlayKeepAlive`: проверить совпадение ID, обновить
  RTT.
- Если предыдущий ID не подтверждён за 30 сек — кикнуть с disconnect
  reason.

**Стоимость:** 1 час. Маленькая, но обязательная.

### 12. Структурированный логгер (`slog`)

**Зачем:** `fmt.Printf` везде; в проде нужны уровни и фильтрация.

- Перейти на `log/slog` (stdlib).
- Уровни: debug, info, warn, error.
- Дефолтный формат: текст; JSON через флаг.

**Стоимость:** 2–3 часа.

### 13. Первая референс-игра

**Зачем:** доказать что plugin API не сломанный.

Простейшая FFA-арена:
- 1 мир (16×16×8 платформа), 4 спавна.
- При входе: kit (меч/еда). Спавн на случайной точке.
- Death = `+1` убийце. Респавн через 3 сек.
- Первый до 10 очков выигрывает.

**Стоимость:** 1–2 дня. Если работает с двумя клиентами без проблем —
движок готов.

---

## Контрольные точки

| Веха | Сигнал что достигнута |
|---|---|
| **Stable foundation** | Шаги 1–3: load-test держит 200 idle игроков без degradation, race detector чист. |
| **Instance MVP** | Шаги 4–7: ручной `Server.MovePlayer(c, hub2)` переводит игрока в другой Instance без disconnect, оба мира видны. |
| **Plugin engine ready** | Шаги 8–9: можно зарегистрировать игру в `games/test/test.go`, она появляется в matchmaker'е. |
| **Реально играбельно** | Шаг 13: FFA-арена работает с двумя клиентами от старта до завершения раунда. |
| **Готовы к парсингу карт** | После всего выше: появится осмысленный сценарий для арен из файлов. |

---

## Следующая большая задача: парсинг карт

После того как движок и плагин-API готовы, главный блокер — **арены вручную
не нарисовать в коде**. Нужен импорт из стандартного формата.

### Цель

Загрузить файл арены, созданный в Minecraft через WorldEdit (или Sponge,
или Litematica), и получить `*world.Template` с блоками и (опционально)
spawn-точками.

### Форматы — выбор

| Формат | Расширение | NBT | Поддержка инструментами | Что несёт |
|---|---|---|---|---|
| **Sponge Schematic v2** | `.schem` | да | WorldEdit (1.13+), FAWE | блоки + block entities + entities + metadata |
| Sponge Schematic v3 | `.schem` | да | новее, не везде | то же + расширения |
| Litematica | `.litematic` | да | мод Litematica | поддерживает несколько регионов в файле |
| Classic Schematic | `.schematic` | да | legacy WorldEdit (< 1.13) | плоский массив block ID — устаревший |
| `.mcstructure` | bedrock | NBT (LE) | Bedrock only | не наш кейс |

**Выбор:** **Sponge v2** (`.schem`) — наиболее распространён, читается
WorldEdit'ом начиная с 1.13, формат стабилен, документирован, держит
block states по namespaced ID (важно для нас — наш `world.Block.Name`
тоже namespaced).

### Структура задачи парсинга

| Подзадача | Содержание | Стоимость |
|---|---|---|
| **NBT Unmarshal** | Сейчас у нас только `Marshal`. Нужен зеркальный `Unmarshal([]byte) (Compound, error)` с поддержкой gzip-обёртки (Sponge файлы gzip-сжаты). | 2–3 дня |
| **Sponge schematic parser** | Прочитать `Width/Height/Length`, `Palette` (mapping name→state ID **в файле**), `BlockData` (varint-stream индексов в палитру). Перенести в наш `world.Block` через имена. | 2–3 дня |
| **Block name registry** | Нужен mapping `"minecraft:stone"` → `world.Block.StateID`. Сейчас у нас 6 констант. На реальную арену надо ~50–200 блоков. **Либо** генерация регистра из vanilla `blocks.json` (есть в `server -reports`), **либо** ручное расширение `world/block.go`. | 1 день (если ручное) до 1–2 недель (если кодеген) |
| **Block entities** | Сундуки, таблички, скаймайнеры. Сейчас не поддерживаем. Для арен — можно сначала игнорировать. | отложить |
| **Spawn points в metadata** | Sponge v2 поддерживает кастомные metadata-теги. Договориться о схеме: например, `Metadata.SpawnPoints: List<{x, y, z, yaw, pitch}>`. Парсить в `Template.SpawnPoints`. | 1 день |
| **CLI tool**: `cmd/import-schem/main.go` | Дев-инструмент: на вход `.schem`, на выход — Go-файл с серилизованным Template (например, `embed`-able бинарь), либо JSON. Чтобы не парсить .schem на старте каждого сервера. | 1 день |
| **Тесты** | Round-trip: построить простой Template вручную → серилизовать в наш формат → распарсить обратно. И парс реального `.schem` файла со стандартной аренки. | 1 день |

**Общая стоимость импорта карт:** 7–10 дней с генерацией block registry,
**4–5 дней** с ручным расширением блоков на старте.

### Что отложить

- **Entities** в schematic'е (моба-spawner'ы и т.п.) — пока без них.
- **Block entities** (NBT блоков типа сундука с предметами внутри) — пока
  без них.
- **Litematica multi-region** — один регион достаточно для арен.
- **Запись `.schem`** (наш мир → файл) — не нужно для игрового сервера.

---

## Что отложено (всё ещё в дорожной карте, но не сейчас)

| Тема | Когда вернёмся |
|---|---|
| Реальный chunk streaming (view distance, динамическая подгрузка) | Когда mini-game'ы заработают и захочется «открытого мира» как один из режимов |
| Persistence (сохранение World на диск между перезапусками) | Когда появятся persistent игры (например, town/build server) |
| Player persistence (инвентари, ачивки) | Вместе с persistence миров |
| `gnet`/`netpoll` (event-loop вместо goroutine-per-conn) | Только если pprof покажет scheduler bottleneck на 5k+ соединениях |
| Update Entity Position вместо Teleport Entity | Когда движение станет реально жирным по трафику |
| Pre-built frame для broadcast | После load-test'а, если zlib окажется горячим |
| Dynamic plugins (Lua/WASM) | Если когда-нибудь захочется отдавать API третьим сторонам |

---

## Что мы НЕ делаем

- **Generic packet builder/DSL** — императивный стиль в `play_send.go`
  читается, любая абстракция здесь раньше времени.
- **Кодеген всех 26 000 block states из `blocks.json` до того, как
  понадобятся** — пока хватает горстки констант, расширяем по мере
  необходимости.
- **External dependencies** — пока обходимся stdlib, и это плюс. Если
  кажется что нужно — пересмотреть задачу.
- **gRPC/REST API для мониторинга** — сервер один, держит автор, логов
  достаточно.
- **Prometheus/метрики** — сначала надо понимать что измерять (это придёт
  из load-test'а).
- **Динамические плагины (Lua/WASM)** до появления реальной потребности —
  internal Go-пакеты с `init()` дают 95% профита без 100% сложности.

---

## TL;DR порядок действий

1. ✅ Load-test harness + pprof.
2. ✅ Per-connection send queue.
3. ✅ Player race fix.
4. ✅ Instance abstraction.
5. ✅ Tick loop per Instance.
5.5. ✅ Интерактивные пакеты (Swing/Interact/UseItemOn/PlayerAction).
6. ✅ Event hooks.
7. ✅ Cross-instance teleport.
8. ✅ World templates + Clone.
9. ✅ `game/` package (Definition, Logic, Registry).
10. ✅ Matchmaker (`/play <game>` + auto-start at MinPlayers).
11. ✅ KeepAlive timeout (atomic config, kick после `KeepAliveTimeout`).
12. ✅ `slog` (LOG_LEVEL/LOG_FORMAT через env).
13. ✅ Референс-игра (FFA arena, `games/ffa`, OnPlayerAttack hook).
14. ✅ Парсинг карт (`.schem` v2/v3 → world.Template, property-aware).
