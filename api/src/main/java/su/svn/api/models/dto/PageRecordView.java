/*
 * This file was last modified at 2026.05.21 16:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PageRecordView.java
 * $Id$
 */

package su.svn.api.models.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;
import java.util.List;

@Deprecated
@JsonInclude(JsonInclude.Include.NON_NULL)
public record PageRecordView(
        @JsonProperty List<RecordView> content,
        @JsonProperty PageableObject pageable,
        @JsonProperty Boolean last,
        @JsonProperty Integer totalPages,
        @JsonProperty Integer totalElements,
        @JsonProperty Boolean first,
        @JsonProperty Integer size,
        @JsonProperty Integer number,
        @JsonProperty SortObject sort,
        @JsonProperty Integer numberOfElements,
        @JsonProperty Boolean empty) implements Serializable {
    @Builder
    public PageRecordView {
    }
}
