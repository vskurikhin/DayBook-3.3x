/*
 * This file was last modified at 2026.05.21 16:48 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * NewBlobRecord.java
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

@JsonPropertyOrder({"visible", "flags"})
@JsonInclude(JsonInclude.Include.NON_NULL)
public record NewBlobRecord(
        @Schema(defaultValue = "00000000-0000-0000-0000-000000000000", requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty UUID parentId,
        @JsonProperty String title,
        @JsonProperty byte[] blob,
        @Schema(requiredMode = Schema.RequiredMode.REQUIRED)
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty boolean visible,
        @JsonProperty int flags,
        @JsonProperty Set<String> tags) implements Serializable {
    @Builder
    public NewBlobRecord {
    }
}
