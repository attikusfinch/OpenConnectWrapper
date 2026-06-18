# OpenConnect Multi

Локальный менеджер профилей для OpenConnect. На Windows приложение открывается как обычное desktop-окно `.exe` через встроенный WebView2, хранит VPN-профили в зашифрованном vault и после ввода 4-значного PIN позволяет быстро переключаться между профилями.

## Что умеет

- Создание нескольких VPN-профилей.
- Быстрое подключение и отключение через локальный UI.
- Хранение логина, пароля, группы, протокола, `servercert`, `useragent` и дополнительных аргументов.
- Шифрование vault через Argon2id + AES-GCM.
- Отдельный `device.key`, без которого `vault.json` не расшифруется.
- Пароль передается в `openconnect` через stdin с `--passwd-on-stdin`.

## Встроенный OpenConnect

В репе лежит Windows amd64 CLI-бинарник OpenConnect:

```text
third_party\openconnect\windows-amd64\openconnect.exe
```

При запуске на Windows приложение сначала ищет bundled бинарник рядом с собой:

```text
dist\openconnect\windows-amd64\openconnect.exe
```

Если bundled бинарника нет, используется `openconnect` из PATH или путь, указанный в настройках UI.

## Запуск

```powershell
go run .\cmd\openconnectmulti
```

После запуска откроется окно приложения. Внутри процесса также поднимается локальный API вида:

```text
http://127.0.0.1:49111
```

Если порт занят, приложение выберет свободный локальный порт. Для отладки можно открыть UI в браузере:

```powershell
.\dist\openconnectmulti.exe --browser
```

## Сборка

Windows:

```powershell
.\scripts\build-windows.ps1
```

Linux:

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o .\dist\openconnectmulti-linux-amd64 .\cmd\openconnectmulti
```

macOS:

```powershell
$env:GOOS="darwin"; $env:GOARCH="arm64"; go build -o .\dist\openconnectmulti-darwin-arm64 .\cmd\openconnectmulti
```

## Где хранится vault

Windows:

```text
%APPDATA%\OpenConnectMulti\vault.json
%APPDATA%\OpenConnectMulti\device.key
```

`vault.json` содержит только зашифрованные данные. `device.key` нужен для расшифровки на этой машине.

## Безопасность

PIN из 4 цифр удобен для ежедневного входа, но это не полноценная замена длинному мастер-паролю. Здесь используется дорогой KDF и отдельный device key, чтобы усложнить офлайн-подбор, но не копируйте весь каталог `%APPDATA%\OpenConnectMulti` на чужие машины и не отправляйте его в чаты.
