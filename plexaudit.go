package main

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/text/message"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

type LibraryItem struct {
	Title         string
	TotalSize     int64
	GUID          string
	CreatedAt     float64
	MetadataID    int
	ElderGUID     string
	NumberStreams int
	LastWatched   float64
	Seasons       []LibraryItemSeason
	Bitrate       float64
}

type LibraryItemSeason struct {
	Title          string
	TotalSize      int64
	MetadataID     int
	ParentID       int
	NumberChildren int
	CreatedAt      float64
	LastWatched    float64
	AvgBitrate     float64
}

type Page int64

const (
	LibraryPicker Page = iota
	LibraryView
)

type model struct {
	page     Page
	cursor   int // which to-do list item our cursor is pointing at
	selected int // which to-do items are selected
	err      error
	table    table.Model
	db       *sql.DB
	printer  *message.Printer

	libraryID            int
	maxLibraryNameLength float64
}

func main() {
	m := model{table: table.Model{}, cursor: 0, selected: 0, err: nil}

	// this query seems to work, but returns less rows than expected
	// seems to stop in 2022
	// select strftime('%Y-%m-%d', mss.created_at, 'unixepoch'), name, title from media_streams ms inner join  media_stream_settings mss on ms.id = mss.media_stream_id inner join  accounts a on a.id = mss.account_id inner join media_items mi on mi.id = ms.media_item_id inner join metadata_items metaitems on metaitems.id = mi.metadata_item_id order by ms.created_at asc;

	// this is the views
	// select strftime('%Y-%m-%d', miv.viewed_at, 'unixepoch'), name, miv.guid, mi.title from metadata_item_views as miv inner join accounts a on a.id = miv.account_id inner join metadata_items mi on mi.guid = miv.guid order by miv.viewed_at asc

	// the items are in metadata_items

	// Connect to the Plex sqlite server

	db, err := sql.Open("sqlite3", "plex.sqlite")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	m.db = db
	m.printer = message.NewPrinter(message.MatchLanguage("en"))

	m.prepareLibraryPickerPage()
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}

}

func (m *model) prepareLibraryPickerPage() {
	tableRows := []table.Row{}

	rows, err := m.db.Query("SELECT library_section_id, name, sum(size) as s FROM media_items INNER JOIN library_sections ls ON ls.id = library_section_id WHERE deleted_at IS NULL AND library_section_id > 0 GROUP BY library_section_id ORDER BY s DESC")
	if err != nil {
		log.Fatal("Media Items query:" + err.Error())
	}
	defer rows.Close()
	// Iterate through the rows
	for rows.Next() {
		var library_section_id int
		var name string
		var size float64
		err := rows.Scan(&library_section_id, &name, &size)
		if err != nil {
			log.Fatal(err)
		}
		sizeInGb := m.printer.Sprintf("%.2f", size/1000.00/1000.00/1000.00)
		m.maxLibraryNameLength = math.Max(m.maxLibraryNameLength, float64(len(name)))
		tableRows = append(tableRows, table.Row{
			fmt.Sprintf("%d", library_section_id),
			name,
			sizeInGb,
		})
	}

	tableColumns := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Library Name", Width: int(math.Max(15, math.Min(36, m.maxLibraryNameLength)))},
		{Title: "Size in Gb", Width: 15},
	}

	t := table.New(
		table.WithColumns(tableColumns),
		table.WithRows(tableRows),
		table.WithFocused(true),
		table.WithHeight(int(math.Max(10, math.Min(30, float64((len(tableRows))))))), // min 10 or (max (30 or number of rows))
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)

	m.table = t

}

func (m *model) prepareLibraryViewPage() {

	//  select size from media_items inner join metadata_items mi on media_items.metadata_item_id = mi.id where size > 0;
	// media_items has size, and binds to metadata_item_id.
	// Once it's to metadata_items, we can remove items that exist in metadata_item_views

	// SELECT mi.title, sum(size) / 1024 / 1024 /1024 FROM media_items INNER JOIN metadata_items mi ON media_items.metadata_item_id = mi.id WHERE size > 0 AND media_items.library_section_id = 3 AND mi.guid NOT IN (SELECT guid FROM metadata_item_views) GROUP BY title order by sum(size) desc;
	// ^ TODO: this needs to group children to the parent

	// SO:
	// Populate a list of all the top-level items in the library:
	query := "SELECT guid, metadata_items.id, title, metadata_items.created_at, coalesce(size,0), coalesce(bitrate, 0) FROM metadata_items LEFT JOIN media_items on media_items.metadata_item_id = metadata_items.id WHERE metadata_items.guid not like 'collection://%' AND parent_id is null and metadata_items.library_section_id = ?;"
	rows, err := m.db.Query(query, m.libraryID)
	if err != nil {
		log.Fatal("MetadataItems query: " + err.Error())
	}
	defer rows.Close()
	// Iterate through the rows

	libraryItems := make(map[string]LibraryItem)
	idToGuidMap := make(map[int]string)
	duplicateGuids := make(map[string]int)
	for rows.Next() {
		var id int
		var title string
		var guid string
		var createdAt float64
		var size int64
		var bitrate float64
		err := rows.Scan(&guid, &id, &title, &createdAt, &size, &bitrate)
		if err != nil {
			log.Fatal(err)
		}
		if libraryItems[guid].MetadataID != 0 {
			duplicateGuids[guid]++
			item := libraryItems[guid]
			item.TotalSize += size
			item.CreatedAt = math.Min(item.CreatedAt, createdAt)
		} else {
			libraryItems[guid] = LibraryItem{Title: title, TotalSize: size, MetadataID: id, NumberStreams: 0, LastWatched: 0, CreatedAt: createdAt, Bitrate: bitrate}
		}

		idToGuidMap[id] = guid
	}

	// for children, we need to sum the media and update the parent
	// start by getting all the seasons
	allSeasons := make(map[int]LibraryItemSeason)
	query = "select season.parent_id, season.id, season.title, sum(size) as size, count(1) as count, avg(bitrate) as avgbitrate FROM media_items INNER JOIN metadata_items episode ON  media_items.metadata_item_id = episode.id INNER JOIN metadata_items season ON season.id = episode.parent_id WHERE episode.library_section_id = ? GROUP BY season.id;"
	rows, err = m.db.Query(query, m.libraryID)
	if err != nil {
		log.Fatal("Views Query: " + err.Error())
	}
	defer rows.Close()
	// Iterate through the rows

	for rows.Next() {
		var parentID int
		var seasonID int
		var title string
		var size int64
		var count int
		var avgBitrate float64
		err := rows.Scan(&parentID, &seasonID, &title, &size, &count, &avgBitrate)
		if err != nil {
			log.Fatal("Children size counting: " + err.Error())
		}

		allSeasons[seasonID] = LibraryItemSeason{Title: title, TotalSize: size, MetadataID: seasonID, ParentID: parentID, NumberChildren: count, AvgBitrate: avgBitrate}
		l := libraryItems[idToGuidMap[parentID]]
		l.TotalSize += size
		l.Seasons = append(l.Seasons, allSeasons[seasonID])
		libraryItems[idToGuidMap[parentID]] = l
	}

	// add view information
	query = "SELECT grandparent_guid, coalesce(size, 0), miv.guid, coalesce(parent_id, 0), miv.viewed_at FROM metadata_item_views as miv INNER JOIN metadata_items mi ON mi.guid = miv.guid LEFT JOIN media_items on media_items.metadata_item_id = mi.id WHERE grandparent_guid is not null and mi.library_section_id = ? and miv.viewed_at > 0 ORDER BY miv.viewed_at ASC;"
	rows, err = m.db.Query(query, m.libraryID)
	if err != nil {
		log.Fatal("Views Query: " + err.Error())
	}
	defer rows.Close()
	// Iterate through the rows

	var oldestView float64
	var newestView float64
	for rows.Next() {

		var guid string
		var parentID int
		var size int64
		var grandparentGuid string
		var viewedAt float64
		err := rows.Scan(&grandparentGuid, &size, &guid, &parentID, &viewedAt)
		if err != nil {
			log.Fatal(err)
		}

		if oldestView == 0 {
			oldestView = viewedAt
		}
		newestView = viewedAt

		if grandparentGuid != "" {
			guid = grandparentGuid
		}

		item := libraryItems[guid]
		//fmt.Println("Upading item", item.Title, item.GUID, item.NumberStreams, viewedAt)
		item.NumberStreams++
		item.LastWatched = math.Max(viewedAt, item.LastWatched)
		libraryItems[guid] = item
	}

	// filter by created and last watched to get the decaying rows
	decayingRows := make([]table.Row, 0)

	// make a unix epoch timestamp for 18 months prior:
	// 18 months = 18 * 30 * 24 * 60 * 60 = 15552000
	outdated := float64(time.Now().AddDate(0, -18, 0).Unix())
	for _, item := range libraryItems {
		if item.CreatedAt > outdated {
			continue
		}

		if item.LastWatched < outdated {

			decayingRows = append(decayingRows, table.Row{
				fmt.Sprintf("%d", item.MetadataID),
				item.Title,
				m.printer.Sprintf("%.2f", float64(item.TotalSize)/1000.00/1000.00/1000.00),
				m.printer.Sprintf("%.1f", item.Bitrate / 1000.0 / 1000.0),
				time.Unix(int64(item.CreatedAt), 0).Format("2006-01-02"),
				time.Unix(int64(item.LastWatched), 0).Format("2006-01-02"),
			})
		} else {
			//color.Green("%s %d", item.Title, item.NumberStreams)
		}
	}

	// format oldestView from a unixepoch timestamp to a date

	oldestViewStr := time.Unix(int64(oldestView), 0).Format("2006-01-02")
	newestViewStr := time.Unix(int64(newestView), 0).Format("2006-01-02")

	_, _ = oldestViewStr, newestViewStr
	//fmt.Println()

	tableColumns := []table.Column{
		{Title: "ID", Width: 10},
		{Title: "Name", Width: int(math.Max(25, math.Min(50, m.maxLibraryNameLength)))},
		{Title: "Size (Gb)", Width: 15},
		{Title: "Bitrate (Mb/s)", Width: 15},
		{Title: "Created", Width: 12},
		{Title: "Last Watched", Width: 12},
	}

	// sort decayingRows by size descending
	sort.Slice(decayingRows, func(i, j int) bool {
		sizeI, _ := strconv.ParseFloat(decayingRows[i][2], 64)
		sizeJ, _ := strconv.ParseFloat(decayingRows[j][2], 64)
		return sizeI > sizeJ
	})

	decayingTable := table.New(
		table.WithColumns(tableColumns),
		table.WithRows(decayingRows),
		table.WithFocused(true),
		table.WithHeight(int(math.Max(10, math.Min(20, float64((len(decayingRows))))))), // min 10 or (max (30 or number of rows))
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	decayingTable.SetStyles(s)

	m.table = decayingTable
}

// host, session, token, and Client-Identifier redacted
// curl 'https://x.plex.direct:32400/playlists/88/items?Item%5Btype%5D=42&Item%5Btitle%5D=Jojo%20Rabbit&Item%5Btarget%5D=Custom%3A%20Universal%20TV&Item%5BtargetTagID%5D=&Item%5BlocationID%5D=-1&Item%5BLocation%5D%5Buri%5D=library%3A%2F%2Fd7a0632c-2227-401b-bea8-f19adeb9c1f9%2Fitem%2F%252Flibrary%252Fmetadata%252F8121&Item%5BDevice%5D%5Bprofile%5D=Universal%20TV&Item%5BPolicy%5D%5Bscope%5D=all&Item%5BPolicy%5D%5Bvalue%5D=&Item%5BPolicy%5D%5Bunwatched%5D=0&Item%5BMediaSettings%5D%5BvideoQuality%5D=60&Item%5BMediaSettings%5D%5BvideoResolution%5D=1920x1080&Item%5BMediaSettings%5D%5BmaxVideoBitrate%5D=8000&Item%5BMediaSettings%5D%5BaudioBoost%5D=&Item%5BMediaSettings%5D%5BsubtitleSize%5D=&Item%5BMediaSettings%5D%5BmusicBitrate%5D=&Item%5BMediaSettings%5D%5BphotoQuality%5D=&Item%5BMediaSettings%5D%5BphotoResolution%5D=&X-Plex-Product=Plex%20Web&X-Plex-Version=4.145.1&X-Plex-Client-Identifier=x&X-Plex-Platform=Firefox&X-Plex-Platform-Version=137.0&X-Plex-Features=external-media%2Cindirect-media%2Chub-style-list&X-Plex-Model=standalone&X-Plex-Device=OSX&X-Plex-Device-Name=Firefox&X-Plex-Device-Screen-Resolution=1388x763%2C1440x900&X-Plex-Token=&X-Plex-Language=en&X-Plex-Session-Id=' --compressed -X PUT -H 'User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:137.0) Gecko/20100101 Firefox/137.0' -H 'Accept: text/plain, */*; q=0.01' -H 'Accept-Language: en' -H 'Accept-Encoding: gzip, deflate, br, zstd' -H 'Origin: https://app.plex.tv' -H 'Sec-GPC: 1' -H 'Connection: keep-alive' -H 'Referer: https://app.plex.tv/' -H 'Sec-Fetch-Dest: empty' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Site: cross-site' -H 'DNT: 1' -H 'Priority: u=0' -H 'Pragma: no-cache' -H 'Cache-Control: no-cache' -H 'Content-Length: 0'

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if m.table.Focused() {
				m.table.Blur()
			} else {
				m.table.Focus()
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.page == LibraryPicker {
				m.libraryID, _ = strconv.Atoi(m.table.SelectedRow()[0])
				m.prepareLibraryViewPage()
				m.page++
			}
			return m, tea.Batch()
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	//fmt.Println("M.page is ", m.page)
	switch m.page {
	case LibraryPicker:
		return baseStyle.Render(m.table.View()) + "\n"
	case LibraryView:

		return baseStyle.Render(m.table.View()) + "\n"
	}
	return "Great."

}
