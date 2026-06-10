import React, { useEffect, useRef, useState } from "react";
import { getRecords } from "../service/records-api";
import styles from './IntersectionObserverRecordsPage.module.scss';
import Record from "./Record/Record";

interface RecordDto {
    id: string;
    title: string;
    markdown?: string;
}

export default function RecordsPage() {
    const [records, setRecords] = useState<RecordDto[]>([]);
    const [page, setPage] = useState(0);
    const [hasMore, setHasMore] = useState(true);
    const loaderRef = useRef<HTMLDivElement | null>(null);
    const loadingRef = useRef(false);

    const loadPage = async (p: number) => {
        if (loadingRef.current || !hasMore) return;

        loadingRef.current = true;

        const data = await getRecords(p, 10);

        setRecords(prev => [...prev, ...(data.list || [])]);

        const total = data.totalRecords || 0;
        if ((p + 1) * 10 >= total) {
            setHasMore(false);
        }

        setPage(p + 1);
        loadingRef.current = false;
    };

    useEffect(() => {
        loadPage(0);
    }, []);

    useEffect(() => {
        const observer = new IntersectionObserver((entries) => {
            if (entries[0].isIntersecting) {
                loadPage(page);
            }
        });

        if (loaderRef.current) observer.observe(loaderRef.current);

        return () => observer.disconnect();
    }, [page]);

    return (
        <div className={styles.container}>
            <div key="root" className={styles.userContainerRoot}>
            </div>
            {records.map(record => (
                <div key={record.id} className={styles.userContainer}>
                    <Record record={record}/>
                </div>
            ))}

            <div ref={loaderRef} style={{ height: 40 }} />
        </div>
    );
}