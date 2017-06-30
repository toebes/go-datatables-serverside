package datatablessrv

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Copyright (c) 2017 Escape Velocity, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.
//
// This package handles the server side processing of an AJAX request for DataTables
// For details on the parameters and the results, read the datatables documentation at
// https://datatables.net/manual/server-side

// SortDir is the direction of the sort (ascending/descending)
type SortDir int

const (
	// Asc for ascending sorting
	Asc SortDir = iota
	// Desc for descending sorting
	Desc
)

// OrderInfo is
type OrderInfo struct {
	ColNum    int // Which column to apply sorting to
	Direction SortDir
}

// ColData is
type ColData struct {
	Name string // columns[i][name] Column's name, as defined by columns.name.
	Data string // columns[i][data] Column's data source, as defined by columns.data.
	// It is poss
	Searchable bool // columns[i][searchable]	boolean	Flag to indicate if this column is searchable (true) or not (false).
	// This is controlled by columns.searchable.
	Orderable bool // columns[i][orderable] Flag to indicate if this column is orderable (true) or not (false).
	// This is controlled by columns.orderable.
	Searchval string // columns[i][search][value] Search value to apply to this specific column.
	UseRegex  bool   // columns[i][search][regex]
	// Flag to indicate if the search term for this column should be treated as regular expression (true) or not (false).
	// As with global search, normally server-side processing scripts will not perform regular expression searching
	// for performance reasons on large data sets, but it is technically possible and at the discretion of your script.
}

// DataTablesInfo is
type DataTablesInfo struct {
	hasFilter bool // There is a filter on the data to apply
	Draw      int  // Draw counter. This is used by DataTables to ensure that the Ajax returns
	// from server-side processing requests are drawn in sequence by DataTables
	// (Ajax requests are asynchronous and thus can return out of sequence).
	// This is used as part of the draw return parameter (see below).
	Start int // Paging first record indicator.
	// This is the start point in the current data set (0 index based - i.e. 0 is the first record).
	Length int // Number of records that the table can display in the current draw.
	// It is expected that the number of records returned will be equal to this number, unless the server has fewer records to return.
	//  Note that this can be -1 to indicate that all records should be returned (although that negates any benefits of server-side processing!)
	Searchval string // Global search value. To be applied to all columns which have searchable as true.
	UseRegex  bool   //	true if the global filter should be treated as a regular expression for advanced searching.
	//  Note that normally server-side processing scripts will not perform regular expression
	//  searching for performance reasons on large data sets, but it is technically possible and at the discretion of your script.
	Order   []OrderInfo
	Columns []ColData
}

// MySQLFilter generates the filter for a mySQL query based on the request and a map of the strings
// to the database entries.  Note if the field is searchable and we don't have a map, this generates
// an error,
// It is assumed that there is a fulltext index on fields in very large tables in order to optimize
// performance of this generated query
// This code will generate a string of the form
//        MATCH(field1,field2,field3) AGAINST('searchval')
//     OR LOCATE(field1,'field1searchval')!=0
//     OR LOCATE(field3,'field3searchval')!=0
// If regular expressions are specified (useRegex) then it will generate a match of
//         field1 REGEXP 'searchval'
//      OR field1 REGEXP 'field1searchval'
//      OR field2 REGEXP 'serchval'
//      OR field3 REGEXP 'searchval'
//      OR field3 REGEXP 'field3searchval'
// Note: If the searchval doesn't actually contain wildcard values (^$.*+|(){}[]?) then the search value bit is actually
// cleared by ParseDatatablesRequest so that we never actually see it
func (di *DataTablesInfo) MySQLFilter(SQLFieldMap map[string]string) (res string, err error) {
	// If there is no filter to be applied, we are done and can skip out
	if !di.hasFilter {
		return
	}
	matchfields := ""
	extramatch := ""
	extra := ""
	query := ""
	for _, coldata := range di.Columns {
		// If this column isn't searchable, then we can just skip to the next field
		if !coldata.Searchable {
			continue
		}
		sqlName, isFound := SQLFieldMap[coldata.Data]
		if !isFound {
			err = fmt.Errorf("Column Data Name %v not found in SQL FieldMap", coldata.Data)
			return
		}
		// Do we have a global search value to match against
		if di.Searchval != "" {
			// For wildcards we have to generate a REGEXP request
			if di.UseRegex {
				query += extra + sqlName + " REGEXP '" + di.Searchval + "'"
				extra = " OR "
			} else {
				// In the non wildcard case, we gather up all the fields
				matchfields += extramatch + sqlName
				extramatch = ","
			}
		}
		// Do we have a search value for just this field
		if coldata.Searchval != "" {
			// If it is a wildcard, we generate a REGEXP request
			if coldata.UseRegex {
				query += extra + sqlName + " REGEXP '" + coldata.Searchval + "'"
			} else {
				// Simple match can use the LOCATE function
				query += extra + "LOCATE(" + sqlName + ", '" + coldata.Searchval + "') > 0"
			}
			extra = " OR "
		}
	}
	if di.Searchval != "" && !di.UseRegex {
		res = " MATCH(" + matchfields + ") AGAINST '" + di.Searchval + "')"
	}
	res += query
	return
}

// MySQLOrderby generates the order by clause for a mySQL query based on the request and a map of the strings
// to the database entries.  Note if the field is orderable and we don't have a map, this generates
// an error
func (di *DataTablesInfo) MySQLOrderby(SQLFieldMap map[string]string) (res string, err error) {
	extra := " ORDER BY "
	// Go through the list of requested items to order
	for _, orderItem := range di.Order {
		// Make sure it refers to one of the columns that was passed in
		if orderItem.ColNum > len(di.Columns) {
			err = fmt.Errorf("Order Column %v out of range %v", orderItem.ColNum, len(di.Columns))
			return
		}
		// Get the data for that column and figure out if the name is one of the fields that we
		// allow in the table
		coldata := di.Columns[orderItem.ColNum]
		sqlName, isFound := SQLFieldMap[coldata.Data]
		if !isFound {
			err = fmt.Errorf("Column Data Name %v not found in SQL FieldMap", coldata.Data)
			return
		}
		res += extra + sqlName
		if orderItem.Direction == Desc {
			res += " DESC"
		}
		extra = ","
	}
	// If we didn't get anything to sort on then we return a simple "ORDER BY 1"
	if res == "" {
		res = " ORDER BY 1"
	}
	return
}

// parseParts takes the split out parts of the field string, verifies that they are
// syntactically valid and then parses them out
//  for example columns[i][search][regex] would come in as
//       field:  'columns[2][search][regex]'
//       nameparts[0]  'columns'
//       nameparts[1]  '2]'
//       nameparts[2]  'search]'
//       nameparts[3]  'regex]'
func parseParts(field string, nameparts []string) (index int, elem1 string, elem2 string, err error) {
	defaultErr := fmt.Errorf("Invalid order[] element %v", field)
	numRegex, err := regexp.Compile("^[0-9]+]$")
	if err != nil {
		return
	}
	elemRegex, err := regexp.Compile("^[a-z]+]$")
	if err == nil {
		return
	}
	if len(nameparts) != 3 && len(nameparts) != 4 {
		err = defaultErr
		return
	}
	// Make sure it is a number followed by the closing ]
	if !numRegex.MatchString(nameparts[1]) {
		err = defaultErr
		return
	}
	// And parse it as a number to make sure
	index, err = strconv.Atoi(nameparts[1])
	if err != nil {
		return
	}
	// Check that the next index is a name token followed by a ]
	if !elemRegex.MatchString(nameparts[2]) {
		err = defaultErr
		return
	}
	// Strip off the trailing ]
	elem1 = strings.TrimSuffix(nameparts[2], "]")
	// If we had a third element, check to make sure it is also close by a ]
	if len(nameparts) == 4 {
		if !elemRegex.MatchString(nameparts[3]) {
			err = defaultErr
			return
		}
		// And trim off the ]
		elem2 = strings.TrimSuffix(nameparts[3], "]")
	}
	// Let's sanity check and make sure they aren't returing an index that is way out of range.
	// We shall assume that no more than 200 columns are being returned
	if index > 200 || index < 0 {
		err = defaultErr
	}
	return
}

// ParseDatatablesRequest checks the HTTP request to see if it corresponds
// to a datatables AJAX data request and parses the request data into
// the DataTablesInfo structure.
func ParseDatatablesRequest(r *http.Request) (res *DataTablesInfo, err error) {
	foundDraw := false
	res = &DataTablesInfo{}
	// Let the request parst the post values into the r.Form structure
	err = r.ParseForm()
	if err != nil {
		return
	}
	for field, value := range r.Form {
		val0 := value[0]
		nameparts := strings.Split(field, "[")
		switch nameparts[0] {
		case "draw":
			foundDraw = true
			res.Draw, err = strconv.Atoi(val0)
		case "start":
			res.Start, err = strconv.Atoi(val0)
		case "length":
			res.Length, err = strconv.Atoi(val0)
		case "search":
			if len(nameparts) != 2 {
				err = fmt.Errorf("Invalid search[] element %v", field)
			} else if nameparts[1] == "value]" {
				res.Searchval = val0
			} else if nameparts[1] == "regex]" {
				res.UseRegex = (val0 == "true")
			} else {
				err = fmt.Errorf("Invalid search[] element %v", field)
			}
		case "order":
			index, elem, _, err := parseParts(field, nameparts)
			if err == nil {
				for len(res.Order) < index {
					res.Order = append(res.Order, OrderInfo{})
				}
				switch elem {
				case "column":
					res.Order[index-1].ColNum, err = strconv.Atoi(val0)
				case "dir":
					if val0 == "desc" {
						res.Order[index-1].Direction = Desc
					} else {
						res.Order[index-1].Direction = Asc
					}
				}
			}
		case "columns":
			index, elem, elem2, err := parseParts(field, nameparts)
			// First make sure we have a
			if err == nil {
				for len(res.Columns) < index {
					res.Columns = append(res.Columns, ColData{})
				}

			}
			switch elem {
			case "data":
				res.Columns[index-1].Data = val0
			case "name":
				res.Columns[index-1].Name = val0
			case "searchable":
				res.Columns[index-1].Searchable = (val0 != "false")
			case "orderable":
				res.Columns[index-1].Orderable = (val0 != "false")
			case "search":
				switch elem2 {
				case "value":
					res.Columns[index-1].Searchval = val0
				case "regex":
					res.Columns[index-1].UseRegex = (val0 != "false")
				}
			}
		}
	}
	// If no Draw was specified in the request, then this isn't a datatables request and we can safely ignore it
	if !foundDraw {
		res = nil
		err = fmt.Errorf("Not a DataTables request")
	}
	res.hasFilter = false
	// We have a valid datatables request, do a tiny bit of optimization. If they requested a wild card
	// but the searchval doesn't have a wild card in it, then we can optimize it to just use regular
	// searching (which will be significantly faster)
	// These are the possible wildcard values (^$.*+|(){}[]?) then the search value bit is actually
	if res.Searchval != "" {
		res.hasFilter = true
		if res.UseRegex && !strings.ContainsAny(res.Searchval, "^$.*+|(){}[]?") {
			res.UseRegex = false
		}
	}
	// Check whether any of the columns have searchable data
	for _, coldata := range res.Columns {
		// If this column is searchable check to make sure it contains something
		if coldata.Searchable && coldata.Searchval != "" {
			// There is something, so we do have a filter
			res.hasFilter = true
			// See if this filter is worthy of a regular expression
			if coldata.UseRegex && !strings.ContainsAny(coldata.Searchval, "^$.*+|(){}[]?") {
				coldata.UseRegex = false
			}
		}
	}
	return
}
