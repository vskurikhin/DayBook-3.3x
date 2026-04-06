/*
 * This file was last modified at 2026.04.06 22:35 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordViewController.java
 * $Id$
 */

package su.svn.core.controllers;

import lombok.AccessLevel;
import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.data.domain.Page;
import org.springframework.data.domain.Pageable;
import org.springframework.http.ResponseEntity;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.ModelAttribute;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import su.svn.core.models.dto.ResourceRecordView;
import su.svn.core.models.dto.ResourceRecordViewFilter;
import su.svn.core.services.domain.RecordViewService;

/**
 * REST controller for retrieving and filtering record views.
 *
 * <p>This controller exposes a read-only endpoint for querying {@link ResourceRecordView}
 * data with optional filtering and pagination support. It delegates the business logic
 * to the {@link RecordViewService}.</p>
 *
 * <p>Base URL: <b>/core/api/v2/records-view</b></p>
 *
 * <h2>Endpoints</h2>
 * <ul>
 *     <li><b>GET /</b> — Retrieve a paginated list of records with optional filters</li>
 * </ul>
 *
 * <h2>Filtering</h2>
 * <p>Filtering is supported via {@link ResourceRecordViewFilter}, which can be passed
 * as query parameters. Supported filters may include fields such as title and date range.</p>
 *
 * <h2>Pagination</h2>
 * <p>Pagination is handled באמצעות Spring's {@link org.springframework.data.domain.Pageable}.
 * Clients can control paging using standard query parameters such as:</p>
 * <ul>
 *     <li><b>page</b> — page number (0-based)</li>
 *     <li><b>size</b> — number of items per page</li>
 *     <li><b>sort</b> — sorting criteria</li>
 * </ul>
 *
 * <h2>Validation</h2>
 * <p>The controller is annotated with {@link org.springframework.validation.annotation.Validated}
 * to support validation of incoming request parameters.</p>
 *
 * <h2>Response</h2>
 * <p>Returns a {@link org.springframework.data.domain.Page} of {@link ResourceRecordView}
 * wrapped in a {@link org.springframework.http.ResponseEntity} with HTTP status {@code 200 OK}.</p>
 *
 * @author Victor N. Skurikhin
 */
@Slf4j
@Validated
@RestController
@RequestMapping("/core/api/v2/records-view")
@RequiredArgsConstructor
@FieldDefaults(level = AccessLevel.PRIVATE, makeFinal = true)
public class RecordViewController {

    /**
     * Service responsible for retrieving and filtering record views.
     */
    RecordViewService recordViewService;

    /**
     * Retrieves a paginated list of record views based on the provided filter and pagination parameters.
     *
     * @param filter   filter criteria for narrowing down results
     * @param pageable pagination and sorting information
     * @return {@link ResponseEntity} containing a page of {@link ResourceRecordView}
     *         and HTTP status {@code 200 OK}
     */
    @GetMapping
    public ResponseEntity<Page<ResourceRecordView>> getAllRecords(
            @ModelAttribute ResourceRecordViewFilter filter, Pageable pageable) {
        Page<ResourceRecordView> records = recordViewService.getFilteredRecords(filter, pageable);
        return ResponseEntity.ok(records);
    }
}
