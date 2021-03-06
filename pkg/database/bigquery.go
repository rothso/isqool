package database

import (
	"cloud.google.com/go/bigquery"
	"context"
	"fmt"
	"github.com/rothso/isqool/pkg/scrape"
	"google.golang.org/api/googleapi"
	"strconv"
	"time"
)

type BigQuery struct {
	ctx     context.Context
	client  *bigquery.Client
	dataset *bigquery.Dataset
}

func NewBigQuery(projectID, datasetID string) (BigQuery, error) {
	var bq BigQuery

	// Set up BigQuery
	ctx := context.Background()
	client, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		return bq, fmt.Errorf("failed to create client: %v", err)
	}

	dataset := client.Dataset(datasetID)
	if err := dataset.Create(ctx, nil); err != nil {
		if !isDuplicateError(err) {
			return bq, fmt.Errorf("failed to create dataset: %v", err)
		}
	}

	bq = BigQuery{ctx, client, dataset}
	return bq, nil
}

func (bq BigQuery) InsertDepartments(departments []scrape.DeptSchedule) error {
	return bq.insert(scrape.DeptSchedule{}, "departments", departments)
}

func (bq BigQuery) InsertISQs(isqs []scrape.CourseIsq) error {
	return bq.insert(scrape.CourseIsq{}, "isqs", isqs)
}

func (bq BigQuery) InsertGrades(grades []scrape.CourseGrades) error {
	return bq.insert(scrape.CourseGrades{}, "grades", grades)
}

func (bq BigQuery) insert(st interface{}, tableName string, data interface{}) error {
	// Infer the table schema
	schema, err := bigquery.InferSchema(st)
	if err != nil {
		return fmt.Errorf("failed to infer schema: %v", err)
	}

	// Get a reference to the table
	table := bq.dataset.Table(tableName)
	if err := table.Create(bq.ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
		if !isDuplicateError(err) {
			return fmt.Errorf("failed to create table: %v", err)
		}
	}

	// Create a temp table
	// Uses a different table each time: https://stackoverflow.com/a/51998193/5623874
	tempName := tableName + "_" + strconv.Itoa(int(time.Now().Unix()))
	newArrivals := bq.dataset.Table(tempName)
	if err := newArrivals.Create(bq.ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
		if !isDuplicateError(err) {
			return fmt.Errorf("failed to create arrivals table: %v", err)
		}
	}

	// Upload data
	u := newArrivals.Inserter()
	if err := u.Put(bq.ctx, data); err != nil {
		return fmt.Errorf("failed to insert rows: %v", err)
	}

	// Merge data
	q := bq.client.Query(`
		MERGE isqool.` + tableName + ` t
		USING isqool.` + tempName + ` s
		ON t.course = s.course
		  AND t.term = s.term
		  AND t.crn = s.crn
		  AND (t.instructor = s.instructor
        	OR IFNULL(t.instructor, s.instructor) IS NULL)
		WHEN NOT MATCHED THEN
		  INSERT ROW`)
	if _, err := q.Run(bq.ctx); err != nil { // TODO return status
		panic(fmt.Errorf("failed to execute query: %v", err))
	}

	// Don't delete the temp table so we can manually audit insertions
	//if err := newArrivals.Delete(bq.ctx); err != nil {
	//	panic(fmt.Errorf("failed to delete arrivals table: %v", err))
	//}

	return nil
}

func isDuplicateError(err error) bool {
	if e, ok := err.(*googleapi.Error); ok {
		return e.Code == 409
	} else {
		return false
	}
}
