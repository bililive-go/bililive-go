export interface Room {
    roomName: string;
    url: string;
}

export interface ItemData {
    key: string,
    name: string,
    room: Room,
    address: string,
    tags: string[],
    listening: boolean
    roomId: string
}
