/*
 * This file was last modified at 2026.05.07 14:57 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PagedModelEntityModelResourceRecordView.java
 * $Id$
 */

package su.svn.api.model.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * PagedModelEntityModelResourceRecordView
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record PagedModelEntityModelResourceRecordView(
        @JsonProperty("_embedded") PagedModelEntityModelResourceRecordViewEmbedded embedded,
        @JsonProperty("_links") Map<String, Link> links,
        @JsonProperty PageMetadata page) implements Serializable {
    @SuppressWarnings("ReassignedVariable")
    @Builder
    public PagedModelEntityModelResourceRecordView {
        if (embedded == null) embedded = new PagedModelEntityModelResourceRecordViewEmbedded();
        if (links == null) links = new LinkedHashMap<>();
    }

    public PagedModelEntityModelResourceRecordView() {
        this(new PagedModelEntityModelResourceRecordViewEmbedded(), new HashMap<>(), /* TODO */ null);
    }
}
