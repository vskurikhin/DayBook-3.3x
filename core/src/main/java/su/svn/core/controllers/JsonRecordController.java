/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordController.java
 * $Id$
 */

package su.svn.core.controllers;

import jakarta.validation.Valid;
import lombok.AccessLevel;
import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.data.crossstore.ChangeSetPersister;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.*;
import su.svn.core.models.dto.NewJsonRecord;
import su.svn.core.models.dto.ResourceJsonRecord;
import su.svn.core.models.dto.UpdateJsonRecord;
import su.svn.core.services.domain.JsonRecordService;

import java.util.UUID;

@Slf4j
@Validated
@RestController
@RequestMapping("/core/api/v2/json-record")
@RequiredArgsConstructor
@FieldDefaults(level = AccessLevel.PRIVATE, makeFinal = true)
public class JsonRecordController {
    JsonRecordService jsonRecordService;

    @PostMapping
    public ResponseEntity<ResourceJsonRecord> createJsonRecord(@RequestBody @Valid NewJsonRecord record) {
        ResourceJsonRecord createdRecord = jsonRecordService.save(record);
        return ResponseEntity.status(HttpStatus.CREATED).body(createdRecord);
    }

    @GetMapping("/{id}")
    public ResponseEntity<ResourceJsonRecord> readJsonRecord(@PathVariable UUID id) throws ChangeSetPersister.NotFoundException {
        ResourceJsonRecord record = jsonRecordService.findById(id);
        return ResponseEntity.status(HttpStatus.OK).body(record);
    }

    @PutMapping
    public ResponseEntity<ResourceJsonRecord> updateJsonRecord(@RequestBody @Valid UpdateJsonRecord record) {
        ResourceJsonRecord updatedRecord = jsonRecordService.update(record);
        return ResponseEntity.ok(updatedRecord);
    }

    @DeleteMapping("/{id}")
    public ResponseEntity<Void> deleteJsonRecord(@PathVariable UUID id) {
        jsonRecordService.disable(id);
        return ResponseEntity.noContent().build();
    }
}
