import React, { useEffect, useState } from 'react';
import { Alert, Button, Icon } from 'antd';

interface Notification {
    id: number;
    type: string;
    message: string;
    status: string;
    created_at: string;
}

const NotificationPanel: React.FC = () => {
    const [notifications, setNotifications] = useState<Notification[]>([]);

    useEffect(() => {
        const fetchNotifications = async () => {
            try {
                const response = await fetch('/api/notifications');
                if (response.ok) {
                    const data = await response.json();
                    setNotifications(data || []);
                }
            } catch (error) {
                console.error("Failed to fetch notifications:", error);
            }
        };

        fetchNotifications();
        const interval = setInterval(fetchNotifications, 10000); // Poll every 10 seconds
        return () => clearInterval(interval);
    }, []);

    const handleResolve = async (id: number) => {
        try {
            const response = await fetch(`/api/notifications/${id}/resolve`, {
                method: 'POST',
            });
            if (response.ok) {
                setNotifications(notifications.filter(n => n.id !== id));
            }
        } catch (error) {
            console.error("Failed to resolve notification:", error);
        }
    };

    if (notifications.length === 0) {
        return null;
    }

    return (
        <div style={{ marginBottom: 16 }}>
            {notifications.map(n => (
                <Alert
                    key={n.id}
                    message={
                        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                            <span>{n.message}</span>
                            <Button
                                type="link"
                                size="small"
                                onClick={() => handleResolve(n.id)}
                            >
                                <Icon type="close" /> Dismiss
                            </Button>
                        </div>
                    }
                    type="error"
                    banner
                    style={{ marginBottom: 8 }}
                />
            ))}
        </div>
    );
};

export default NotificationPanel;
