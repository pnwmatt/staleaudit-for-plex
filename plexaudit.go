package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/fatih/color"
)

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

func main() {

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
	// Query the database

	/*
		select library_section_id, name, sum(size) as s, (sum(size) / 1024  / 1024 / 1024) from media_items inner join library_sections ls on ls.id = library_section_id where deleted_at is null and library_section_id > 0 group by library_section_id order by s desc;
		3|TV Shows|5375784943307|5006
		2|Movies|3264685815029|3040
		1|Music|42894312856|39
		4|DJ Sets|4746035793|4
		5|Halloween|1458775298|1
	*/

	color.Cyan("# Library Sizes")
	rows, err := db.Query("SELECT library_section_id, name, sum(size) as s, (sum(size) / 1024  / 1024 / 1024) FROM media_items INNER JOIN library_sections ls ON ls.id = library_section_id WHERE deleted_at IS NULL AND library_section_id > 0 GROUP BY library_section_id ORDER BY s DESC")
	if err != nil {
		log.Fatal("Media Items query:" + err.Error())
	}
	defer rows.Close()
	// Iterate through the rows
	for rows.Next() {
		var library_section_id int
		var name string
		var size int
		var size_gb int
		err := rows.Scan(&library_section_id, &name, &size, &size_gb)
		if err != nil {
			log.Fatal(err)
		}
		color.Green("%d %s %d %d", library_section_id, name, size, size_gb)
	}

	//  use stdin to get the library section id:
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
	rows, err = db.Query(query, libraryChoice)
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

	query = "SELECT coalesce(grandparent_guid, miv.guid), miv.viewed_at FROM metadata_item_views as miv INNER JOIN metadata_items mi ON mi.guid = miv.guid WHERE grandparent_guid is not null and mi.library_section_id = ? and miv.viewed_at > 0 ORDER BY miv.viewed_at ASC;"
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
		var viewedAt int64
		err := rows.Scan(&guid, &viewedAt)
		if oldestView == 0 {
			oldestView = viewedAt
		}
		newestView = viewedAt
		if err != nil {
			log.Fatal(err)
		}

		item := libraryItems[guid]
		item.NumberStreams++
		item.LastWatched = viewedAt
		libraryItems[guid] = item
	}

	for _, item := range libraryItems {

		if item.NumberStreams == 0 {
			color.Red("%s %d", item.Title, item.NumberStreams)
		} else {
			color.Green("%s %d", item.Title, item.NumberStreams)
		}
	}

	// format oldestView from a unixepoch timestamp to a date

	oldestViewStr := time.Unix(oldestView, 0).Format("2006-01-02")
	newestViewStr := time.Unix(newestView, 0).Format("2006-01-02")
	fmt.Println()
	color.Cyan("Oldest View: %s", oldestViewStr)
	color.Cyan("Newest View: %s", newestViewStr)

}
