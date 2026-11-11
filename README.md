# spotify-playlist-cli

Spotify Web API を叩くためだけの薄い CLI です。コマンド名は `spcli` です。

プレイリストの取得とトラックの追加だけを行います。
重複チェック、定期実行、選曲の判断、通知は呼び出し側の責務とし、この CLI では扱いません。

- 正常時は JSON を stdout に出力します
- エラー時は JSON を stderr に出力し、非0で終了します
- 認証は Authorization Code フローで行い、本人の Spotify アプリ（機密クライアント）を使います

## インストール

### GitHub Releases

[Releases](https://github.com/Soli0222/spotify-playlist-cli/releases) から各プラットフォーム向けのバイナリを取得できます。
Linux x86_64 で `~/.local/bin` に置く場合は次のとおりです。

```bash
gh release download --repo Soli0222/spotify-playlist-cli \
  --pattern 'spotify-playlist-cli_Linux_x86_64.tar.gz' --output - \
  | tar -xz -C ~/.local/bin spcli
spcli version
```

### Go install

```bash
go install github.com/soli0222/spotify-playlist-cli/cmd/spcli@latest
```

### ソースからビルド

```bash
git clone https://github.com/Soli0222/spotify-playlist-cli.git
cd spotify-playlist-cli
make build    # ./spcli が生成される
```

## セットアップ

### 1. Spotify Developer Dashboard

[Spotify Developer Dashboard](https://developer.spotify.com/dashboard) でアプリを作成します。

| 項目 | 値 |
|---|---|
| App name | `spotify-playlist-cli` |
| Redirect URI | `http://127.0.0.1:8765/callback` |
| Which API/SDKs are you planning to use? | Web API |

作成後、Settings から Client ID と Client Secret を控えます。

Development Mode のアプリには次の制約があります。

- アプリのオーナーが Spotify Premium を契約している必要があります
- オーナー以外のアカウントで使う場合は、User Management にそのアカウントを登録します

### 2. 環境変数

Client ID と Client Secret は環境変数から読みます。
リポジトリ、シェル履歴、ログに値を残さないでください。

| 環境変数 | 内容 |
|---|---|
| `SPOTIFY_CLIENT_ID` | Dashboard の Client ID |
| `SPOTIFY_CLIENT_SECRET` | Dashboard の Client Secret |
| `XDG_CONFIG_HOME` | 任意。トークンの保存先の基点（既定は `~/.config`） |

`auth login` と、アクセストークンのリフレッシュ時に Client ID と Client Secret を使います。
アクセストークンは約1時間で失効するため、各コマンドを実行する環境にも設定しておきます。

### 3. 認証

ブラウザがある端末では、ローカルでコールバックを受けて自動で完了します。

```bash
spcli auth login
```

リモートサーバーなどブラウザがない環境では `--headless` を使います。

```bash
spcli auth login --headless
```

1. stderr に表示された認可 URL を、手元のブラウザで開いて許可します
2. ブラウザが `http://127.0.0.1:8765/callback?code=...&state=...` へリダイレクトされます。ページは表示できなくて構いません
3. アドレスバーの URL 全体（または `code` の値だけ）をターミナルに貼り付けて Enter を押します

認可画面は毎回表示されます（`show_dialog=true`）。
要求するスコープは `playlist-read-private`、`playlist-modify-public`、`playlist-modify-private` です。

### 4. 認証状態の確認

```bash
spcli auth status
```

```json
{"authenticated":true,"user":{"id":"soli","display_name":"Soli"},"scopes":["playlist-read-private","playlist-modify-public","playlist-modify-private"],"missing_scopes":[],"expires_at":"2026-11-11T13:00:00Z","expires_in_seconds":3540,"refresh_token_stored":true,"token_file":"/home/soli/.config/spotify-playlist-cli/token.json"}
```

`missing_scopes` が空でなければ、`spcli auth login` をやり直します。
トークンや Client Secret の値は出力しません。

## コマンド

```
spcli auth login [--headless]
spcli auth status
spcli playlist get <playlist>
spcli playlist items <playlist> [--limit N] [--offset N]
spcli playlist add <playlist> <track> [<track> ...] [--dry-run]
spcli version
```

### 入力形式

`<playlist>` と `<track>` は次の形式を受け付けます。
CLI 側で ID に正規化してから API を呼び出します。

| 形式 | プレイリストの例 | トラックの例 |
|---|---|---|
| ID | `7xUQAlcjqXAnPin5jyeOHZ` | `0CmpgRM57Uq8KOQAzKOxrq` |
| URI | `spotify:playlist:7xUQAlcjqXAnPin5jyeOHZ` | `spotify:track:0CmpgRM57Uq8KOQAzKOxrq` |
| URL | `https://open.spotify.com/playlist/7xUQAlcjqXAnPin5jyeOHZ?si=...` | `https://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq` |
| ロケール付き URL | `https://open.spotify.com/intl-ja/playlist/...` | `https://open.spotify.com/intl-ja/track/0CmpgRM57Uq8KOQAzKOxrq?si=...` |

`spotify.link` の短縮 URL は受け付けません。
不正な入力は API を呼ぶ前に終了コード 2 で拒否します。

zsh では `?` がグロブとして解釈されるため、クエリ付き URL は引用符で囲みます。

```bash
spcli playlist add 7xUQAlcjqXAnPin5jyeOHZ 'https://open.spotify.com/intl-ja/track/0CmpgRM57Uq8KOQAzKOxrq?si=bd303f89bf084bee'
```

### `playlist get`

プレイリストのメタデータを出力します。
`total_items` は、認証ユーザーが所有または共同編集しているプレイリストでだけ値が入ります。それ以外は `null` です。

```json
{"id":"7xUQAlcjqXAnPin5jyeOHZ","uri":"spotify:playlist:7xUQAlcjqXAnPin5jyeOHZ","url":"https://open.spotify.com/playlist/7xUQAlcjqXAnPin5jyeOHZ","name":"プレイリスト #26","description":null,"public":false,"collaborative":false,"snapshot_id":"...","owner":{"id":"soli","display_name":"Soli"},"total_items":1}
```

### `playlist items`

プレイリスト内の要素を出力します。
既定では全件を返し、ページングは内部で処理します（1リクエスト50件）。
`--limit` と `--offset` を指定すると、その範囲だけを返します。

```json
{"playlist_id":"7xUQAlcjqXAnPin5jyeOHZ","total":1,"offset":0,"count":1,"items":[{"position":0,"type":"track","id":"0CmpgRM57Uq8KOQAzKOxrq","uri":"spotify:track:0CmpgRM57Uq8KOQAzKOxrq","name":"曲名","artists":[{"id":"...","name":"アーティスト名"}],"album":"アルバム名","added_at":"2026-11-11T12:00:00Z","is_local":false}]}
```

| フィールド | 内容 |
|---|---|
| `total` | プレイリスト全体の件数 |
| `count` | 今回返した件数 |
| `items[].position` | プレイリスト内の位置（0始まり） |
| `items[].type` | `track`、`episode`、`unavailable`（削除済みなどで中身がない要素） |
| `items[].id` | ローカルファイルや `unavailable` では `null` |

この API は、認証ユーザーが所有または共同編集しているプレイリストでだけ使えます。

### `playlist add`

指定したトラックをプレイリストの末尾に追加します。
重複チェックはしません。同じトラックを複数指定すると、その数だけ追加します。
複数指定は1リクエストにまとめ、100件を超える場合は100件ずつに分割して順に送ります。

```json
{"dry_run":false,"playlist_id":"7xUQAlcjqXAnPin5jyeOHZ","count":1,"uris":["spotify:track:0CmpgRM57Uq8KOQAzKOxrq"],"requests":[{"uris":["spotify:track:0CmpgRM57Uq8KOQAzKOxrq"],"snapshot_id":"..."}],"snapshot_id":"..."}
```

`--dry-run` では API を呼ばず、認証も不要です。
送信予定のプレイリスト ID と正規化後のトラック URI だけを出力します。

```json
{"dry_run":true,"playlist_id":"7xUQAlcjqXAnPin5jyeOHZ","count":1,"uris":["spotify:track:0CmpgRM57Uq8KOQAzKOxrq"],"requests":[{"uris":["spotify:track:0CmpgRM57Uq8KOQAzKOxrq"]}]}
```

分割送信の途中で失敗した場合、エラーの `details` に成功済みの件数（`added_count`）と URI（`added_uris`）を含めます。

## エラー

エラーは次の形式で stderr に出力します。

```json
{"error":{"kind":"insufficient_scope","status":403,"message":"Insufficient client scope","hint":"...","details":{"granted_scopes":["playlist-read-private"],"missing_scopes":["playlist-modify-public","playlist-modify-private"]}}}
```

| 終了コード | `kind` | 意味 |
|---|---|---|
| 1 | `api_error` / `network_error` / `internal` | その他の API エラー、通信エラー、内部エラー |
| 2 | `usage` / `invalid_argument` | 引数不足、不正なフラグ、不正な ID / URI / URL |
| 3 | `not_authenticated` | トークンが保存されていない |
| 3 | `unauthorized` | API が 401 を返した（トークンの失効・取り消し） |
| 3 | `auth_failed` | 認可の拒否、state の不一致、トークンの交換・リフレッシュの失敗 |
| 3 | `missing_client_credentials` | `SPOTIFY_CLIENT_ID` / `SPOTIFY_CLIENT_SECRET` が未設定 |
| 4 | `insufficient_scope` | 403 `Insufficient client scope`（スコープ不足） |
| 5 | `forbidden` | スコープ不足以外の 403 |
| 6 | `not_found` | 404 |
| 7 | `rate_limited` | 429 がリトライ後も続いた |

403 は本文が `Insufficient client scope` のときだけ `insufficient_scope` とします。
それ以外の 403 は `forbidden` とし、スコープ不足とは区別します。
`forbidden` の主な原因は、所有・共同編集していないプレイリストへの操作、Development Mode の User Management に未登録のアカウント、アプリのオーナーの Premium 失効です。
どちらの場合も、`details` に保存済みトークンの付与スコープと不足スコープを含めます。

429 のときは `Retry-After` に従って待ち、最大3回までリトライします。
`Retry-After` が60秒を超える場合は待たずに `rate_limited` で終了し、`details.retry_after_seconds` に待ち時間を入れます。
429 以外のエラーはリトライしません。

## トークンの保存

トークンは `~/.config/spotify-playlist-cli/token.json`（`XDG_CONFIG_HOME` があればその下）に、パーミッション 0600 で保存します。
ディレクトリは 0700 で作成します。

各コマンドの実行時に有効期限を確認し、残りが60秒を切っていればリフレッシュしてから API を呼び出します。
応答に新しいリフレッシュトークンが含まれていれば保存し直します。

## 開発

```bash
make test
make lint
make build
```

使用している Spotify Web API のエンドポイントは次のとおりです。

| 用途 | エンドポイント |
|---|---|
| アカウント | `GET /v1/me` |
| プレイリスト | `GET /v1/playlists/{id}` |
| 要素の取得 | `GET /v1/playlists/{id}/items` |
| 要素の追加 | `POST /v1/playlists/{id}/items` |
| 認可・トークン | `GET https://accounts.spotify.com/authorize`、`POST https://accounts.spotify.com/api/token` |

旧来の `/v1/playlists/{id}/tracks` は廃止されたため使いません。
要素のフィールドは `item` を使い、廃止予定の `track` は `item` がない応答のときだけ読みます。

## リリース

Conventional Commits のコミットを `main` に入れると、release-please がリリース PR を作成します。
リリース PR をマージするとタグと GitHub Release が作られ、GoReleaser がバイナリを添付します。
保留中のリリース PR は、毎週日曜 07:00 JST に CI が通っていれば自動でマージします。
