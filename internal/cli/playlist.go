package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
	"github.com/soli0222/spotify-playlist-cli/internal/spotify"
	"github.com/soli0222/spotify-playlist-cli/internal/spotifyid"
)

type playlistOutput struct {
	ID            string      `json:"id"`
	URI           string      `json:"uri"`
	URL           string      `json:"url"`
	Name          string      `json:"name"`
	Description   *string     `json:"description"`
	Public        *bool       `json:"public"`
	Collaborative bool        `json:"collaborative"`
	SnapshotID    string      `json:"snapshot_id"`
	Owner         ownerOutput `json:"owner"`
	// TotalItems は所有・共同編集していないプレイリストでは null。
	TotalItems *int `json:"total_items"`
}

type ownerOutput struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"display_name"`
}

type itemsOutput struct {
	PlaylistID string       `json:"playlist_id"`
	Total      int          `json:"total"`
	Offset     int          `json:"offset"`
	Count      int          `json:"count"`
	Items      []itemOutput `json:"items"`
}

type itemOutput struct {
	Position int    `json:"position"`
	Type     string `json:"type"`
	// ID はローカルファイルや利用できない要素では null。
	ID      *string        `json:"id"`
	URI     string         `json:"uri"`
	Name    string         `json:"name"`
	Artists []artistOutput `json:"artists"`
	Album   *string        `json:"album"`
	Show    *string        `json:"show,omitempty"`
	AddedAt string         `json:"added_at"`
	IsLocal bool           `json:"is_local"`
}

type artistOutput struct {
	ID   *string `json:"id"`
	Name string  `json:"name"`
}

type addOutput struct {
	DryRun     bool                `json:"dry_run"`
	PlaylistID string              `json:"playlist_id"`
	Count      int                 `json:"count"`
	URIs       []string            `json:"uris"`
	Requests   []spotify.AddResult `json:"requests"`
	SnapshotID string              `json:"snapshot_id,omitempty"`
}

func newPlaylistCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "playlist",
		Short: "プレイリストの取得・追加",
		Long: "プレイリストの取得・追加。\n" +
			"<playlist> はプレイリスト ID、spotify:playlist:<ID>、https://open.spotify.com/playlist/<ID> のいずれでもよい。",
	}
	cmd.AddCommand(newPlaylistGetCmd(app))
	cmd.AddCommand(newPlaylistItemsCmd(app))
	cmd.AddCommand(newPlaylistAddCmd(app))
	return cmd
}

func parsePlaylistArg(arg string) (string, error) {
	id, err := spotifyid.ParsePlaylist(arg)
	if err != nil {
		return "", apperr.New(apperr.KindInvalidArgument, "%s", err.Error()).WithDetail("input", arg)
	}
	return id, nil
}

func newPlaylistGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <playlist>",
		Short: "プレイリストのメタデータを表示する",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parsePlaylistArg(args[0])
			if err != nil {
				return err
			}
			mgr, err := app.tokenManager()
			if err != nil {
				return err
			}
			p, err := app.apiClient(mgr.AccessToken).GetPlaylist(cmd.Context(), id)
			if err != nil {
				return withScopeDetails(err, mgr.Current())
			}
			out := playlistOutput{
				ID:            p.ID,
				URI:           p.URI,
				URL:           p.ExternalURLs["spotify"],
				Name:          p.Name,
				Description:   p.Description,
				Public:        p.Public,
				Collaborative: p.Collaborative,
				SnapshotID:    p.SnapshotID,
				Owner:         ownerOutput{ID: p.Owner.ID, DisplayName: p.Owner.DisplayName},
			}
			if p.Items != nil {
				total := p.Items.Total
				out.TotalItems = &total
			}
			return writeJSON(app.Stdout, out)
		},
	}
}

func newPlaylistItemsCmd(app *App) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "items <playlist>",
		Short: "プレイリスト内のトラックを表示する（既定は全件。内部でページングする）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parsePlaylistArg(args[0])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("limit") {
				if limit < 1 {
					return apperr.New(apperr.KindInvalidArgument, "--limit must be 1 or greater")
				}
			} else {
				limit = -1
			}
			if offset < 0 {
				return apperr.New(apperr.KindInvalidArgument, "--offset must be 0 or greater")
			}
			mgr, err := app.tokenManager()
			if err != nil {
				return err
			}
			items, total, err := app.apiClient(mgr.AccessToken).ListPlaylistItems(cmd.Context(), id, limit, offset)
			if err != nil {
				return withScopeDetails(err, mgr.Current())
			}
			out := itemsOutput{PlaylistID: id, Total: total, Offset: offset, Count: len(items), Items: make([]itemOutput, 0, len(items))}
			for i, it := range items {
				out.Items = append(out.Items, toItemOutput(offset+i, it))
			}
			return writeJSON(app.Stdout, out)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "取得する最大件数（省略時は offset 以降の全件）")
	cmd.Flags().IntVar(&offset, "offset", 0, "取得を開始する位置（0始まり）")
	return cmd
}

func toItemOutput(position int, it spotify.PlaylistItem) itemOutput {
	out := itemOutput{
		Position: position,
		AddedAt:  it.AddedAt,
		IsLocal:  it.IsLocal,
		Artists:  []artistOutput{},
	}
	p := it.Playable()
	if p == nil {
		// 削除済み・利用不可の要素。
		out.Type = "unavailable"
		return out
	}
	out.Type = p.Type
	out.ID = p.ID
	out.URI = p.URI
	out.Name = p.Name
	for _, a := range p.Artists {
		out.Artists = append(out.Artists, artistOutput{ID: a.ID, Name: a.Name})
	}
	if p.Album != nil {
		name := p.Album.Name
		out.Album = &name
	}
	if p.Show != nil {
		name := p.Show.Name
		out.Show = &name
	}
	return out
}

func newPlaylistAddCmd(app *App) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add <playlist> <track> [<track> ...]",
		Short: "トラックをプレイリストの末尾に追加する（重複チェックはしない）",
		Long: "トラックをプレイリストの末尾に追加する。重複チェックはしない。\n" +
			"<track> はトラック ID、spotify:track:<ID>、https://open.spotify.com/track/<ID>、" +
			"https://open.spotify.com/intl-ja/track/<ID>?si=... のいずれでもよい。\n" +
			"100件を超える場合は100件ずつに分割して送る。--dry-run では API を叩かず送信予定の内容だけを表示する。",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parsePlaylistArg(args[0])
			if err != nil {
				return err
			}
			uris := make([]string, 0, len(args)-1)
			var invalid []map[string]string
			for _, in := range args[1:] {
				trackID, err := spotifyid.ParseTrack(in)
				if err != nil {
					invalid = append(invalid, map[string]string{"input": in, "reason": err.Error()})
					continue
				}
				uris = append(uris, spotifyid.URI(spotifyid.TypeTrack, trackID))
			}
			if len(invalid) > 0 {
				msgs := make([]string, 0, len(invalid))
				for _, v := range invalid {
					msgs = append(msgs, v["reason"])
				}
				return apperr.New(apperr.KindInvalidArgument, "%d invalid track(s): %s", len(invalid), strings.Join(msgs, "; ")).
					WithDetail("invalid", invalid)
			}

			if dryRun {
				requests := []spotify.AddResult{}
				for _, chunk := range spotify.Chunk(uris, spotify.MaxItemsPerAdd) {
					requests = append(requests, spotify.AddResult{URIs: chunk})
				}
				return writeJSON(app.Stdout, addOutput{DryRun: true, PlaylistID: id, Count: len(uris), URIs: uris, Requests: requests})
			}

			mgr, err := app.tokenManager()
			if err != nil {
				return err
			}
			results, err := app.apiClient(mgr.AccessToken).AddItems(cmd.Context(), id, uris)
			if err != nil {
				err = withScopeDetails(err, mgr.Current())
				if e := apperr.As(err); e != nil {
					added := []string{}
					for _, r := range results {
						added = append(added, r.URIs...)
					}
					e.SetDetail("playlist_id", id)
					e.SetDetail("added_count", len(added))
					e.SetDetail("added_uris", added)
					e.SetDetail("requested_count", len(uris))
				}
				return err
			}
			out := addOutput{PlaylistID: id, Count: len(uris), URIs: uris, Requests: results}
			if len(results) > 0 {
				out.SnapshotID = results[len(results)-1].SnapshotID
			}
			return writeJSON(app.Stdout, out)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "API を叩かず、プレイリスト ID と正規化後のトラック URI 一覧だけを表示する")
	return cmd
}
