/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordView.java
 * $Id$
 */

package su.svn.api.model.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record RecordView(
        @JsonProperty UUID id,
        @JsonProperty Boolean visible,
        @JsonProperty Integer flags,
        @JsonProperty UUID parentId,
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty LocalDateTime lastChangedTime,
        @JsonProperty String title,
        @JsonProperty Map<String, String> values) implements Serializable {
    @Builder
    public RecordView {
    }
}
