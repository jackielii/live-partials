package main

import "github.com/jackielii/live-partials/examples/labdata"

// The lab's data and simulation are shared with examples/datastar.
type (
	store         = labdata.Store
	series        = labdata.Series
	seriesSummary = labdata.SeriesSummary
	person        = labdata.Person
	draftRow      = labdata.DraftRow
	file          = labdata.File
	burstRow      = labdata.BurstRow
	job           = labdata.Job
)

var errNotFound = labdata.ErrNotFound
