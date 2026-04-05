/*
 * This file was last modified at 2026.04.04 13:04 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordData.java
 * $Id$
 */

package su.svn.api.model.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;
import su.svn.api.domain.entities.RecordType;

import java.io.Serializable;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record RecordData(
        @JsonProperty UUID id,
        @JsonProperty UUID parentId,
        @JsonProperty RecordType type,
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty Boolean visible,
        @JsonProperty int flags,
        @JsonProperty String title,
        @JsonProperty Map<String, String> values) implements Serializable {
    @Builder
    public RecordData {
    }
}
