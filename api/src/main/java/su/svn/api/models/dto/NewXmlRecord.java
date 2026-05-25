/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * NewXmlRecord.java
 * $Id$
 */

package su.svn.api.models.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.annotation.JsonPropertyOrder;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Builder;

import java.io.Serializable;
import java.time.OffsetDateTime;
import java.util.Set;
import java.util.UUID;

/**
 * DTO representing a request for creating a new XML record.
 *
 * <p>
 * This object is used as an input payload for XML resource creation APIs.
 * It contains XML content, metadata, timestamps, visibility settings,
 * and optional tags.
 * </p>
 *
 * <h2>JSON Serialization</h2>
 * <ul>
 *     <li>Null fields are excluded from JSON output</li>
 *     <li>{@code visible} and {@code flags} are serialized last</li>
 * </ul>
 *
 * @param parentId parent record identifier
 * @param title optional record title
 * @param xml XML document content
 * @param postAt publication timestamp
 * @param visible visibility flag
 * @param flags custom flags
 * @param tags optional tag collection
 */
@JsonPropertyOrder({"visible", "flags"})
@JsonInclude(JsonInclude.Include.NON_NULL)
public record NewXmlRecord(
        @Schema(defaultValue = "00000000-0000-0000-0000-000000000000", requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty UUID parentId,
        @JsonProperty String title,
        @Schema(requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty String xml,
        @Schema(requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty boolean visible,
        @JsonProperty int flags,
        @JsonProperty Set<String> tags) implements Serializable {
    @Builder
    public NewXmlRecord {
    }
}
