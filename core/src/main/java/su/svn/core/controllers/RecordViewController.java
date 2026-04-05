/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
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

@Slf4j
@Validated
@RestController
@RequestMapping("/core/api/v2/records-view")
@RequiredArgsConstructor
@FieldDefaults(level = AccessLevel.PRIVATE, makeFinal = true)
public class RecordViewController {
    RecordViewService recordViewService;

    @GetMapping
    public ResponseEntity<Page<ResourceRecordView>> getAllRecords(
            @ModelAttribute ResourceRecordViewFilter filter, Pageable pageable) {
        Page<ResourceRecordView> records = recordViewService.getFilteredRecords(filter, pageable);
        return ResponseEntity.ok(records);
    }
}
