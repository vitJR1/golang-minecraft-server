# DOCS

Разработческая документация. Этот файл — про **как пользоваться** уже
готовыми примитивами сервера. Архитектурный контекст — в `CLAUDE.md`,
планы — в `plan.md`.

---

## Instance event hooks

Хуки — точка интеграции игровой логики со внутренностями сервера. Каждый
`*Instance` несёт набор function-полей; присвой `nil` (по умолчанию) —
поведение «всё разрешено / no-op», присвой функцию — она вызывается в
заранее определённой точке.

### Доступные хуки

```go
type Instance struct {
    OnPlayerJoin  func(c *ClientConnection)
    OnPlayerLeave func(c *ClientConnection)
    OnBlockBreak  func(c *ClientConnection, pos world.Position) bool
    OnBlockPlace  func(c *ClientConnection, pos world.Position, block world.Block) bool
    OnChat        func(c *ClientConnection, msg string) (rewrite string, allow bool)
}
```

### Где они вызываются

| Хук             | Триггер                                                                                          | Когда именно                                  |
|-----------------|--------------------------------------------------------------------------------------------------|-----------------------------------------------|
| `OnPlayerJoin`  | После всех visibility-пакетов в `Instance.JoinAndAnnounce`                                       | Игрок виден остальным, остальные видны игроку |
| `OnPlayerLeave` | В `Instance.LeaveAndAnnounce`, **перед** Remove из PlayerList                                    | Игрок ещё в списке `i.Players`                |
| `OnBlockPlace`  | В `handler_play` для `SbPlayUseItemOnBlock`, **после Ack**, **перед** `SetBlock`                 | До изменения мира                             |
| `OnBlockBreak`  | В `handler_play` для `SbPlayPlayerAction` (action=0/2), **после Ack**, **перед** `SetBlock(Air)` | До изменения мира                             |
| `OnChat`        | В `handler_play` для `SbPlayChatMessage`, **перед** `BroadcastChat`                              | До рассылки                                   |

### Семантика return-значений

| Хук                  | Return           | Значение                                                                                         |
|----------------------|------------------|--------------------------------------------------------------------------------------------------|
| `OnPlayerJoin/Leave` | (void)           | Уведомление, отменить нельзя                                                                     |
| `OnBlockBreak/Place` | `bool`           | `true` = разрешить, `false` = отменить, клиент получит откат через Block Update с прежним блоком |
| `OnChat`             | `(string, bool)` | rewrite + allow. `allow=false` дропает сообщение, `rewrite` заменяет текст                       |

### Примеры

#### Spawn protection — нельзя ломать ниже Y=60

```go
arena := server.NewInstance("arena", srv, world.NewMemoryWorld())
arena.OnBlockBreak = func(c *server.ClientConnection, pos world.Position) bool {
    if pos.Y < 60 {
        return false  // veto
    }
    return true
}
```

При попытке сломать блок под уровнем 60: сервер пошлёт Ack + Block Update
с исходным блоком → клиент откатит свою prediction, блок останется.

#### Цензура чата

```go
arena.OnChat = func(c *server.ClientConnection, msg string) (string, bool) {
    if strings.Contains(strings.ToLower(msg), "spoiler") {
        return "", false  // не пускаем в чат
    }
    return msg, true
}
```

#### Замена текста (например, добавить префикс роли)

```go
arena.OnChat = func(c *server.ClientConnection, msg string) (string, bool) {
    return "[player] " + msg, true
}
```

Получатели увидят `<Name> [player] hello`.

#### Welcome-сообщение и spawn-кит на входе

```go
arena.OnPlayerJoin = func(c *server.ClientConnection) {
    arena.BroadcastChat("", fmt.Sprintf("§e%s joined the arena", c.PlayerName()))
    // Здесь же выдать стартовый kit, телепортнуть на spawn, и т.д.
    // (после того как добавим inventory + Server.MovePlayer)
}
```

#### Уборка состояния при выходе

```go
type gameState struct {
    scores map[int32]int
    mu     sync.Mutex
}

arena.OnPlayerLeave = func(c *server.ClientConnection) {
    state.mu.Lock()
    delete(state.scores, c.EntityID())
    state.mu.Unlock()
}
```

#### Запрет ставить блоки за пределами арены

```go
arena.OnBlockPlace = func(c *server.ClientConnection, pos world.Position, block world.Block) bool {
    if pos.X < -50 || pos.X > 50 || pos.Z < -50 || pos.Z > 50 {
        return false
    }
    return true
}
```

### Гарантии

- **Recover вокруг каждого вызова.** Паника в хуке логируется (`instance
  <id> <name> hook panic: ...`), но не падает goroutine. Это касается и
  tick handlers, и event hooks.
- **Хуки вне `joinMu`.** Внутри хука можно безопасно звать `i.SetBlock`,
  `i.BroadcastChat`, `i.Players.Range(...)` — не будет deadlock'а с
  логикой Join/Leave.
- **Set-once.** Хуки задаются при создании инстанса. После того как игроки
  начали в нём появляться — не переприсваивай, нет синхронизации на этих
  полях. Если нужна динамическая логика — оберни в свою функцию-диспатчер,
  которая внутри переключает поведение.

### Ограничения / гочи

- **Хук вызывается, но `c.player`/`c.PlayerName()` нельзя дёргать в
  `OnPlayerLeave` если соединение упало во время handshake.** Защищены
  проверкой `c.player == nil` в `LeaveAndAnnounce` — хук не вызывается в
  этом случае. Но если хук получил вызов, `c.player` гарантированно не nil.
- **Блокирующая логика в хуке тормозит обработку пакета.** `OnChat`
  вызывается синхронно перед broadcast'ом; долгий хук задержит чат у всех.
  Если нужна асинхронная работа — `go func() { ... }()` внутри хука.
- **`OnBlockBreak/Place` веро после Ack.** Это значит клиент УЖЕ
  подтверждён, и rollback идёт через отдельный Block Update. Если хук
  тормозит, клиент успевает увидеть свою prediction и потом отменить —
  пара кадров визуального glitch'а. В обычных условиях незаметно.
- **Хук не знает про другие хуки.** Сейчас у каждого хука один subscriber.
  Если игре нужна цепочка обработчиков — собирай руками внутри своего
  хука.

### Что НЕЛЬЗЯ делать из хуков (пока)

- Звать `Server.MovePlayer(c, ...)` (cross-instance teleport) — он ещё не
  реализован.
- Изменять `c.instance` напрямую — поле без синхронизации.
- Менять сами хуки на том же инстансе во время работы — race на field.

Когда `MovePlayer` появится (план #7), его можно будет звать из хуков —
он будет правильно синхронизирован.

### Что ещё хочется (потом)

- `OnPlayerDeath(c, killer)` — после реализации damage system.
- `OnInteract(c, targetEID, type)` — после реализации PvP.
- `OnInventoryClick(c, slot, item)` — после реализации inventory.

---

## Tick loop

Каждый `Instance` крутит свой 20 Hz tick loop (`TickRate = 20`,
`tickInterval = 50ms`). Подписка:

```go
arena.OnTick(func(tick uint64) {
    if tick % 20 == 0 {
        // раз в секунду
    }
    if tick == startTick + 600 {
        // через 30 секунд после startTick — конец раунда
    }
})
```

- Можно регистрировать несколько подписчиков (`OnTick` можно звать
  многократно).
- Snapshot подписчиков снимается перед итерацией → handler может
  регистрировать новые, не паникуя.
- **Slow handler дропает следующий тик** — `time.Ticker` не очередит
  накопления. Игровое время визуально растягивается, не сжимается.
- Recover внутри каждого handler — паника одного не убивает loop.

`i.Tick()` возвращает текущий счётчик. `i.Stop()` останавливает loop
(idempotent через `sync.Once`).

---

## Команды

Команды лежат в `server/commands.go`. Регистрация в `init()`:

```go
registerCommand(&Command{
    Name:    "mycommand",
    Aliases: []string{"mc"},
    NeedsOp: true,                            // или false для общедоступной
    Help:    "/mycommand <arg> — описание",
    Run:     cmdMyCommand,
})
```

Обработчик:

```go
func cmdMyCommand(c *ClientConnection, args []string) {
    if len(args) != 1 {
        _ = c.sendSystemMessage("Usage: /mycommand <arg>")
        return
    }
    // делать дело
    _ = c.sendSystemMessage("Done")
}
```

Dispatcher (`Server.RunCommand`) сам проверяет права и шлёт «Unknown
command» / «You don't have permission».

### Currently registered

| Команда                              | Aliases     | Op? | Описание                           |
|--------------------------------------|-------------|-----|------------------------------------|
| `/op <player>`                       | —           | да  | Выдать operator                    |
| `/deop <player>`                     | —           | да  | Забрать operator                   |
| `/gamemode <s/c/a/sp> [player]`      | `/gm`       | да  | Сменить gamemode (себе или игроку) |
| `/tp <player>` или `/tp <x> <y> <z>` | `/teleport` | да  | Телепорт (внутри инстанса)         |
| `/help`                              | —           | нет | Список команд                      |

---

## Chat

`Instance.BroadcastChat(sender, message)` рассылает строку
`<sender> message` всем в инстансе. Пустой `sender` → нет угловых скобок
(серверное объявление).

`c.sendSystemMessage(text)` — личное сообщение одному игроку (для
ответов команд).

Под капотом — SystemChat (`Cb 0x64`) с JSON-кодированной chat component.

---

## Когда нужны новые packets

1. Найти ID в wiki.vg для **protocol 763 (1.20.1)** — версия зашита в коде.
2. Добавить константу в `server/packet_ids.go` под секцию Sb/Cb + state.
3. Для **Sb**: добавить `case` в `handler_play.go` (или другом state'е).
4. Для **Cb**: написать `sendXxx`-функцию в `play_send.go` (или
   `visibility.go` для broadcasts).
5. Тест с реальным клиентом — если приходит `IndexOutOfBoundsException`
   от netty, значит ID или формат пакета неверный.

ID-таблица в wiki.vg разбита по версиям — для 1.20.1 это `oldid=18375`
(закреплено в комментарии в `packet_ids.go`).
