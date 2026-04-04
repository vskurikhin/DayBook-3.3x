/*
 * This file was last modified at 2026.04.04 15:56 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ResourceRecordView.java
 * $Id$
 */

package su.svn.core.models.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.annotation.JsonPropertyOrder;
import lombok.Builder;

import java.io.Serializable;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;

@JsonPropertyOrder({"id", "visible", "flags"})
@JsonInclude(JsonInclude.Include.NON_NULL)
public record ResourceRecordView(
        @JsonProperty UUID id,
        @JsonProperty UUID parentId,
        @JsonIgnore String userName,
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty LocalDateTime lastChangedTime,
        @JsonProperty boolean visible,
        @JsonProperty int flags,
        @JsonProperty String title,
        @JsonProperty Map<String, String> values
) implements Serializable {
    @Builder(toBuilder = true)
    public ResourceRecordView {
    }
}
