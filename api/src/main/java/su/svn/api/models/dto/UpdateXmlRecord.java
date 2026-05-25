/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * UpdateXmlRecord.java
 * $Id$
 */

package su.svn.api.models.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Builder;

import java.io.Serializable;
import java.time.OffsetDateTime;
import java.util.Set;
import java.util.UUID;

/**
 * DTO representing an update request for an existing XML record.
 *
 * <p>
 * This payload is used for updating XML resource properties,
 * including XML content, metadata, timestamps, visibility flags,
 * and associated tags.
 * </p>
 *
 * <h2>Validation Rules</h2>
 * <ul>
 *     <li>{@code id} is required</li>
 *     <li>{@code xml} is required</li>
 *     <li>{@code refreshAt} is required</li>
 * </ul>
 *
 * @param id unique resource identifier
 * @param parentId parent resource identifier
 * @param type record type
 * @param title optional title
 * @param xml XML document content
 * @param postAt publication timestamp
 * @param refreshAt refresh timestamp
 * @param visible visibility flag
 * @param flags custom flags
 * @param tags optional tag collection
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record UpdateXmlRecord(
        @Schema(requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty UUID id,
        @Schema(defaultValue = "00000000-0000-0000-0000-000000000000", requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty UUID parentId,
        @JsonProperty su.svn.lib.RecordType type,
        @JsonProperty String title,
        @Schema(requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty String xml,
        @JsonProperty OffsetDateTime postAt,
        @Schema(requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty boolean visible,
        @JsonProperty int flags,
        @JsonProperty Set<String> tags) implements Serializable {
    @Builder
    public UpdateXmlRecord {
    }
}
