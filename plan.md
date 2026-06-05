# План развития

Дорожная карта к полноценному mini-game серверу. Не контракт — пересматриваем
после каждого шага. Версия протокола: **763 (1.20.1)**. Stdlib only.

---

## Где мы сейчас

Базовый mini-game движок **готов и работает**. Закрыты все 14 пунктов
исходного roadmap'а (история — в конце файла, секция «Сделано»). Коротко,
что есть на руках:

- **Сеть**: протокол от handshake до play, compression (zlib threshold 256),
  online-mode (Mojang auth + AES-CFB8), offline-mode. Per-connection send
  queue (`writerLoop` + `net.Buffers`), KeepAlive timeout enforcement.
- **Instance abstraction**: каждый «room» (хаб, лобби, раунд) — свой мир,
  свой `PlayerList`, свой 20 Hz tick loop, свой broadcast-scope. Хаб — такой
  же Instance. Cross-instance teleport (`Server.MovePlayer`) переносит игрока
  без disconnect.
- **Plugin API** (`game/`): `Definition` + `Logic` + `Ctx` + `PlayerHandle`,
  адаптеры в `server/game_bridge.go`. Игры живут в `games/` и регистрируются
  через `init()`, не трогая ядро.
- **Matchmaker**: `/play <game>`, авто-старт при `MinPlayers`, очереди.
- **Интерактивные пакеты**: Swing/Interact/UseItemOn/PlayerAction + event
  hooks (`OnBlockBreak/Place`, `OnChat`, `OnPlayerAttack`, `OnPlayerJoin/Leave`).
- **World templates**: `.schem` v2/v3 → `world.Template`, property-aware.
- **Хаб-навигация**: blaze-rod → сундук-GUI с выбором игры (`hub_menu.go`),
  лобби FFA/BedWars/SkyWars (`lobbies.go`), ender-pearl → меню арен.
- **Модерация/идентичность**: ops, ban (durations), mute, chat-moderator,
  offline-auth (`auth.json`, /register + /login).
- **Референс-игра**: `games/ffa` — FFA-арена, проверяет, что plugin API не
  сломан.

`go test -race ./...` — чисто.

### Честная оценка: что движок ещё НЕ умеет

Эти пробелы всплыли при подготовке к party/friends/мини-играм. Они —
фундамент, без которого новые фичи либо невозможны, либо строятся на гонках:

1. **Нет способа выполнить действие на горутине чужого соединения.**
   `MovePlayer` обязан вызываться из readLoop-горутины самого игрока
   (`server.go`, комментарий над `MovePlayer`). Matchmaker обходит это через
   `go MovePlayer(...)` (гонка), а `EndGame` в `game_bridge.go` прямо
   расписывается: «accept the race». Чтобы затащить **всю party** в игру или
   телепортнуть друга — нужен безопасный per-connection канал команд.
2. **Нет боевой системы.** `OnPlayerAttack` чисто информативный; FFA фейкает
   килл одним кликом. Нет HP, урона, смерти, knockback, респауна.
3. **Нет модели инвентаря/китов.** `giveHotbarItem` — ad-hoc Set Container
   Slot; click-container исходит из «реальных трансферов нет».
4. **Меню арены — тупик.** `arenaOnClick` (`hub_menu.go`) только логирует
   выбор; в matchmaker не диспатчит. Хаб рекламирует BedWars/SkyWars, которых
   ещё нет.
5. **Нет persistent player-identity store.** ban/mutes/ops/auth.json есть, но
   для friends нужен отдельный store отношений, переживающий рестарт.
6. **Matchmaker не держит группу вместе** — забирает из очереди FIFO, нет
   понятия «эти N игроков — в один инстанс» (нужно для party).

---

## Архитектурный фундамент (делаем ПЕРЕД фичами)

В порядке выполнения. Каждый пункт автономный.

### 15. Per-connection task queue

**Зачем:** единственный безопасный способ выполнить `MovePlayer` (и любую
мутацию состояния соединения) для *чужого* игрока. Разблокирует party-warp,
matchmaker без `go MovePlayer`, корректный `EndGame`.

- `tasks chan func()` на `ClientConnection` + выделенная drainer-горутина
  (select на `tasks` и `done`), запускается в `HandleConn` рядом с
  `writerLoop`/`keepAlive`.
- `c.instance` → `atomic.Pointer[Instance]` (сейчас «нет синхронизации»,
  читается в каждом play-пакете, пишется в `MovePlayer`).
- `MovePlayer` вызывается **только** из task-горутины → сериализован
  per-connection. Matchmaker и `EndGame` переводятся на `c.Enqueue(fn)`.
- Закрытие: drain + дренаж канала в `cleanup`, идемпотентно.

**Стоимость:** 2–3 дня. Тесты на конкурентный move + disconnect.

### 16. Боевая система (HP / урон / смерть / knockback / респаун)

**Зачем:** фундамент для FFA-real, BedWars, SkyWars. Сейчас бой — фикция.

- `player.Player`: `health float32` (0..20), опц. `food`, под тем же `mu`.
- Клиентские пакеты (см. `packet_ids.go` / wiki.vg 763):
  - **Set Health (Cb)** — HP/еда/saturation.
  - **Damage Event** или Entity Animation (hurt) — визуальный удар по жертве.
  - **Set Entity Velocity (Cb)** — knockback (вектор из yaw атакующего +
    вертикальный бамп).
  - **Combat Death / Death combat event (Cb)** — экран смерти.
  - **Client Command (Sb)** — обработать «perform respawn».
- `OnPlayerAttack` → реальный путь урона: attack-cooldown, base damage,
  расчёт knockback, применение к жертве. При HP ≤ 0 — смерть.
- Новый хук **`OnPlayerDeath(victim, killer)`** на `Instance` и в `game.Logic`
  (сейчас в интерфейсе только `OnPlayerAttack`). `NoopLogic` — дефолт.
- Респаун: `Player.Reset()` (HP/положение) + телепорт на spawn игры.
- **Void/fall death**: tick-проверка Y < порога (нужно для Spleef/SkyWars) и
  опц. fall-distance урон.

**Стоимость:** 5–7 дней. Самая мясная часть; тестировать живым клиентом.

### 17. Инвентарь и киты

**Зачем:** FFA-real хочет меч, BedWars/SkyWars — броню/блоки/ресурсы.
Обобщает `giveHotbarItem`.

- Серверная модель инвентаря (44 слота) на `ClientConnection` или `Player`.
- `Kit` — список `{slot, itemID, count, nbt}`, применяется на spawn через
  Set Container Content (window 0).
- Shop-GUI для BedWars/SkyWars **переиспользует** существующий `openMenu` /
  chest-GUI паттерн (`hub_menu.go`) — отдельный движок транзакций не нужен.
- Реальный drag-n-drop / Click Container транзакции — **откладываем**: игры
  идут на Adventure/Survival с локнутым инвентарём + chest-shop.

**Стоимость:** 3–4 дня.

### 18. Командная абстракция (teams)

**Зачем:** только для BedWars (и опц. командных режимов). Цвета, общий spawn,
friendly-fire off, командный tab/чат.

- `Team` на уровне `Instance` (или внутри game.Logic): id, цвет, состав,
  spawn, статус (жива/выбита).
- Teams (Cb) packet — цвет ника/коллизии/nametag в tab-list.
- `OnPlayerAttack`/`OnPlayerDamage` уважает friendly-fire флаг команды.

**Стоимость:** 2–3 дня. Можно делать прямо внутри `games/bedwars`, если не
выносить в ядро.

### 19. Persistent player-identity store (для friends)

**Зачем:** дружба переживает рестарт; ключ — стабильный UUID
(`OfflineUUID` для offline, Mojang UUID для online).

- Пакет `friends/` по образцу `ban`/`mutes`: `friends.json`, Load/Save/reload,
  атомарная запись.
- Модель: запрос → подтверждение (mutual). Хранить `map[uuid] -> set[uuid]` +
  pending-requests.
- API: `Add/Request/Accept/Deny/Remove/List/AreFriends`.

**Стоимость:** 1–2 дня (store) — UI отдельно в треке friends.

### 20. Matchmaker: party-aware + проводка меню арен

**Зачем:** закрыть тупик меню и научить очередь держать группу вместе.

- `arenaOnClick` / `hubMainOnClick` → `Matchmaker.Queue` (вместо лог-заглушки).
  Иконка/арена → gameID.
- `Matchmaker.QueueParty([]*ClientConnection, gameID)` — группа как единица:
  попадает в один инстанс целиком, отклоняется если `len(party) > MaxPlayers`.
- Снять временный `go MovePlayer` в `startGame` — переход на `c.Enqueue` (п.15).

**Стоимость:** 2–3 дня.

---

## Трек A: сами мини-игры

Каждая — пакет в `games/`, регистрация через `init()`, мир из `.schem`
template (см. `schem/templates/<game>/`). Зависят от боевой системы (16) и
инвентаря (17), кроме Parkour.

### 21. FFA-real

**Зачем:** переписать референс на настоящий бой — доказать движок 16+17.

- Использовать HP/урон/смерть вместо «клик = килл».
- Kit: меч + еда (17). Респаун через 16. Счёт по реальным киллам.
- Спавн-защита (короткая неуязвимость после респауна).

**Стоимость:** 1–2 дня (поверх 16/17).

### 22. Spleef

**Зачем:** лёгкая игра на уже существующих хуках, проверяет void-death.

- `OnBlockBreak` ломает снег/блок под ногами (мгновенно, без дропа).
- Падение в void (16, Y-порог) = выбывание. Последний оставшийся побеждает.
- Не требует боевой HP-системы — только void-death из 16.

**Стоимость:** 1–2 дня.

### 23. Parkour

**Зачем:** игра вообще без боя/инвентаря — раньше всех (не блокируется 16/17).

- Чекпойнты + таймер на `OnTick`: детект позиции игрока в зоне.
- Старт/финиш, личное время, /parkour reset.
- Падение → телепорт на последний чекпойнт.

**Стоимость:** 1–2 дня. Можно делать параллельно фундаменту.

### 24. BedWars

**Зачем:** флагман. Команды (18), кровати, генераторы ресурсов, shop (17).

- 2–4 команды, у каждой кровать + spawn + остров.
- `OnBlockBreak` кровати чужой команды → команда теряет респаун.
- Tick-генераторы: периодический спавн железа/золота на базах.
- Shop через chest-GUI (17): покупка блоков/брони/оружия за ресурсы.
- Победа: последняя команда с живыми игроками.

**Стоимость:** 7–10 дней. Самая большая. Делать после 16/17/18.

### 25. SkyWars

**Зачем:** второй флагман. Острова, лут-сундуки, void-death.

- N островов-спавнов из template, центральный остров.
- Сундуки с лутом (loot table) — раздача предметов на старте раунда.
- Void-death (16). PvP до последнего выжившего.
- Опц.: chest-refill в середине раунда (tick).

**Стоимость:** 5–7 дней. После 16/17.

---

## Трек B: Party (объединение в группы)

Зависит от per-connection task queue (15) для warp.

### 26. Party core + команды

- `Party` (server-side, эфемерная): leader, members, pending-invites.
  `PartyManager` на `Server` (как `Matchmaker`).
- Команды: `/party invite <player>`, `/party accept <leader>`,
  `/party leave`, `/party kick <player>`, `/party list`, `/party disband`.
- Party-чат scope (`/party chat` или префикс) — отдельный broadcast по
  членам, поверх `FindPlayer` (кросс-instance).
- Очистка в `cleanup` на disconnect (как `Matchmaker.Dequeue`).

**Стоимость:** 3–4 дня.

### 27. Party ↔ matchmaker / warp

- `/party warp` — leader тащит всю party в свой текущий инстанс
  (через `c.Enqueue` + `MovePlayer`, п.15).
- При `/play <game>` лидером — вся party встаёт в очередь как группа
  (`QueueParty`, п.20) и попадает в один раунд.
- При входе лидера в лобби/игру через меню — party следует за ним.

**Стоимость:** 2–3 дня.

---

## Трек C: Friends (реально полезный список друзей)

Зависит от identity-store (19) и party (26).

### 28. Friends store + команды

- Поверх `friends/` (19).
- Команды: `/friend add <player>`, `/friend accept <player>`,
  `/friend deny <player>`, `/friend remove <player>`, `/friend list`,
  `/friend requests`.
- Презенс: онлайн/оффлайн + где сейчас (инстанс) через `FindPlayer`.
- Уведомления: друг зашёл/вышел (опц., с подавлением спама).

**Стоимость:** 2–3 дня.

### 29. Friends menu + invite-to-party (главная цель трека)

**Зачем:** ради этого всё — открыть меню и пригласить друга в party кликом.

- Новый предмет в хабе (напр. «голова игрока» / бумага) → chest-GUI
  «Друзья» (переиспользуем `openMenu` из `hub_menu.go`).
- Слот на друга: голова/иконка + ник + статус (онлайн/в игре X/оффлайн).
- Клик по онлайн-другу → действие: **пригласить в party** (создаёт party
  если её нет) **или** «прыгнуть к нему» (`/party warp` наоборот — join к
  его инстансу). Меню действий — второй экран chest-GUI.
- Pending-запросы дружбы — отдельная страница меню (accept/deny кликом).

**Стоимость:** 3–4 дня. Завязано на 26/27 (invite создаёт/пополняет party).

---

## Контрольные точки

| Веха                | Сигнал что достигнута                                                                                    |
|---------------------|----------------------------------------------------------------------------------------------------------|
| **Фундамент готов** | 15–17: можно безопасно двигать чужого игрока; удар снимает HP, смерть → респаун; кит выдаётся на спавне. |
| **Бой играбелен**   | 21: FFA-real проходит раунд на реальном HP с двумя клиентами.                                            |
| **Командные игры**  | 24: BedWars — ломаем кровать, команда без респауна, последняя команда побеждает.                         |
| **Party работает**  | 27: лидер `/party warp` тащит группу в инстанс; `/play` ставит всю party в один раунд.                   |
| **Friends-цель**    | 29: открыл меню друзей → кликнул онлайн-друга → он получил инвайт в party → вместе зашли в игру.         |

---

## Что отложено (всё ещё в дорожной карте, но не сейчас)

| Тема                                                             | Когда вернёмся                                                   |
|------------------------------------------------------------------|------------------------------------------------------------------|
| Реальные Click-Container транзакции (drag-n-drop инвентаря)      | Если игре понадобится свободный инвентарь, а не kit + chest-shop |
| Реальный chunk streaming (view distance, динамическая подгрузка) | Когда захочется «открытого мира» как режима                      |
| Persistence миров (сохранение World на диск)                     | Когда появятся persistent-игры (town/build)                      |
| Player persistence (инвентари, ачивки, статы)                    | Вместе с persistence миров; статистика по играм                  |
| Друзья: оффлайн-инвайты / уведомления при заходе                 | После базового friends-меню (29)                                 |
| `gnet`/`netpoll` (event-loop вместо goroutine-per-conn)          | Только если pprof покажет scheduler bottleneck на 5k+            |
| Update Entity Position вместо Teleport Entity                    | Когда движение станет жирным по трафику                          |
| Dynamic plugins (Lua/WASM)                                       | Если захочется отдавать API третьим сторонам                     |

---

## Что мы НЕ делаем

- **Generic packet builder/DSL** — императивный стиль в `play_send.go`
  читается, абстракция тут преждевременна.
- **Кодеген всех block states до того, как понадобятся** — расширяем по мере.
- **External dependencies** — обходимся stdlib.
- **gRPC/REST/Prometheus для мониторинга** — сервер один, логов достаточно
  (метрики придут из load-test'а, если понадобятся).
- **Античит** — не на этом этапе; mini-game арены контролируемые.

---

## Сделано (история roadmap'а)

Исходный план целился сначала в «один большой мир», потом развернулся в
mini-game платформу. Все 14 пунктов закрыты:

1. ✅ Load-test harness + pprof (`cmd/loadtest`).
2. ✅ Per-connection send queue (`writerLoop`, `net.Buffers`).
3. ✅ Player race fix (`sync.RWMutex` в `player.Player`).
4. ✅ Instance abstraction (`server/instance.go`).
5. ✅ Tick loop per Instance (20 Hz).
5.5. ✅ Интерактивные пакеты (Swing/Interact/UseItemOn/PlayerAction).
6. ✅ Event hooks (`OnBlockBreak/Place`, `OnChat`, `OnPlayerAttack`, join/leave).
7. ✅ Cross-instance teleport (`Server.MovePlayer`).
8. ✅ World templates + Clone (`world.Template`).
9. ✅ `game/` package (Definition, Logic, Registry, Ctx, PlayerHandle).
10. ✅ Matchmaker (`/play <game>` + auto-start at MinPlayers).
11. ✅ KeepAlive timeout enforcement.
12. ✅ `slog` (LOG_LEVEL/LOG_FORMAT через env).
13. ✅ Референс-игра (`games/ffa`, OnPlayerAttack hook).
14. ✅ Парсинг карт (`.schem` v2/v3 → `world.Template`, property-aware).

**Парсинг карт** (бывшая «следующая большая задача») закрыт: NBT Unmarshal с
gzip, Sponge schematic parser, block-name registry, spawn-points из metadata,
CLI-инструмент. Запись `.schem`, block entities и Litematica multi-region —
сознательно не делаем (для арен не нужно).