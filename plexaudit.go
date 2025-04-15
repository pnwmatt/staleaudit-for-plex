package main

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/text/message"

	"github.com/fatih/color"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))


type LibraryItem struct {
	Title         string
	TotalSize     int
	GUID          string
	CreatedAt     int64
	MetadataID    int
	ElderGUID     string
	NumberStreams int
	LastWatched   int64

	
}

type Page int64

const (
	LibraryPicker Page = iota
	LibraryView
)

type model struct {
	page Page
    cursor   int                // which to-do list item our cursor is pointing at
    selected int  // which to-do items are selected
	err error
	table table.Model
	db *sql.DB
	printer *message.Printer

	libraryID int
	maxLibraryNameLength float64

}


func main() {
	m := model{table: table.Model{}, cursor: 0, selected: 0, err: nil }

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


	tableColumns, tableRows := m.getLibrariesAndSizes()

	t := table.New(
		table.WithColumns(tableColumns),
		table.WithRows(tableRows),
		table.WithFocused(true),
		table.WithHeight(int(math.Max(10,math.Min(30, float64((len(tableRows))))))), // min 10 or (max (30 or number of rows))
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
	

	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
	
	libraryChoice := 0
	fmt.Print("Enter the library section id: ")
	fmt.Scanf("%d", &libraryChoice)

	color.Cyan("# Unwatched Content")
	//  select size from media_items inner join metadata_items mi on media_items.metadata_item_id = mi.id where size > 0;
	// media_items has size, and binds to metadata_item_id.
	// Once it's to metadata_items, we can remove items that exist in metadata_item_views

	// SELECT mi.title, sum(size) / 1024 / 1024 /1024 FROM media_items INNER JOIN metadata_items mi ON media_items.metadata_item_id = mi.id WHERE size > 0 AND media_items.library_section_id = 3 AND mi.guid NOT IN (SELECT guid FROM metadata_item_views) GROUP BY title order by sum(size) desc;
	// ^ TODO: this needs to group children to the parent

	// SO:
	// Populate a list of all the top-level items in the library:

	query := "SELECT guid, id, title, created_at FROM metadata_items where parent_id is null and library_section_id = ?;"
	rows, err := db.Query(query, libraryChoice)
	if err != nil {
		log.Fatal("MetadataItems query: " + err.Error())
	}
	defer rows.Close()
	// Iterate through the rows

	libraryItems := make(map[string]LibraryItem)
	for rows.Next() {
		var id int
		var title string
		var guid string
		var createdAt int64
		err := rows.Scan(&guid, &id, &title, &createdAt)
		if err != nil {
			log.Fatal(err)
		}
		if libraryItems[guid].MetadataID != 0 {
			log.Fatal("Duplicate ID found in library items")
		}
		libraryItems[guid] = LibraryItem{Title: title, TotalSize: 0, MetadataID: id, NumberStreams: 0, LastWatched: 0, CreatedAt: createdAt}
	}

	query = "SELECT grandparent_guid, miv.guid, miv.viewed_at FROM metadata_item_views as miv INNER JOIN metadata_items mi ON mi.guid = miv.guid WHERE grandparent_guid is not null and mi.library_section_id = ? and miv.viewed_at > 0 ORDER BY miv.viewed_at ASC;"
	rows, err = db.Query(query, libraryChoice)
	if err != nil {
		log.Fatal("Views Query: " + err.Error())
	}
	defer rows.Close()
	// Iterate through the rows

	var oldestView int64
	var newestView int64
	for rows.Next() {

		var guid string
		var grandparentGuid string
		var viewedAt int64
		err := rows.Scan(&grandparentGuid, &guid, &viewedAt)
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
		item.LastWatched = viewedAt
		libraryItems[guid] = item
	}

	// make a unix epoch timestamp for 18 months prior:
	// 18 months = 18 * 30 * 24 * 60 * 60 = 15552000
	outdated := time.Now().AddDate(0, -18, 0).Unix()
	for _, item := range libraryItems {
		if item.CreatedAt > outdated {
			continue
		}

		if item.LastWatched < outdated {
			color.Red("%s %d", item.Title, item.NumberStreams)
		} else {
			//color.Green("%s %d", item.Title, item.NumberStreams)
		}
	}

	// format oldestView from a unixepoch timestamp to a date

	oldestViewStr := time.Unix(oldestView, 0).Format("2006-01-02")
	newestViewStr := time.Unix(newestView, 0).Format("2006-01-02")
	fmt.Println()
	color.Cyan("Oldest View: %s", oldestViewStr)
	color.Cyan("Newest View: %s", newestViewStr)

}

func (m model) getLibrariesAndSizes() ([]table.Column, []table.Row) {
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
		sizeInGb := m.printer.Sprintf("%.2f", size / 1024.00 / 1024.00 / 1024.00)
		m.maxLibraryNameLength = math.Max(m.maxLibraryNameLength, float64(len(name)))
		tableRows = append(tableRows, table.Row{
			fmt.Sprintf("%d", library_section_id),
			name,
			sizeInGb,
		})
	}

	tableColumns := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Library Name", Width: int(math.Max(15, math.Min(36,m.maxLibraryNameLength)))},
		{Title: "Size in GB", Width: 15},
	}

	return tableColumns, tableRows
}


func (m model) Init() tea.Cmd { return nil }

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
				m.page++
			}
			return m, tea.Batch(
				tea.Printf("Let's go to %s!", m.table.SelectedRow()[1]),
			)
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	return baseStyle.Render(m.table.View()) + "\n"
}